package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	nats "github.com/nats-io/nats.go"
)

// NATSMessage represents an opaque HTTP message for NATS transport
type NATSMessage struct {
	RequestID string `json:"request_id"`
	ReplyTo   string `json:"reply_to,omitempty"` // Only set for requests
	HTTPBytes []byte `json:"http_bytes"`         // Raw HTTP wire format
	IsRequest bool   `json:"is_request"`         // true=request, false=response
	Error     string `json:"error,omitempty"`    // Transport-level errors only
}

// Config holds configuration for the NATS HTTP transport
type Config struct {
	NATSURLs       []string      // NATS server URLs
	Credentials    nats.Option   // Authentication (optional)
	ServiceName    string        // Service name for routing (Phase 2, unused for now)
	Timeout        time.Duration // Default timeout
	ClientID       string        // Unique client identifier (auto-generated if empty)
	ReplyStreamTTL time.Duration // Reply stream cleanup time (default: 1h)
	ConsumerGroup  string        // Consumer group name for agents
}

// Transport implements http.RoundTripper for NATS-based HTTP transport
type Transport struct {
	nc       *nats.Conn
	js       nats.JetStreamContext
	config   Config
	clientID string
}

func NewTransport(config Config) (*Transport, error) {
	// Set defaults
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.ClientID == "" {
		config.ClientID = uuid.New().String()
	}

	// Connect to NATS
	var opts []nats.Option
	if config.Credentials != nil {
		opts = append(opts, config.Credentials)
	}

	url := nats.DefaultURL
	if len(config.NATSURLs) > 0 {
		url = config.NATSURLs[0]
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create JetStream context
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	transport := &Transport{
		nc:       nc,
		js:       js,
		config:   config,
		clientID: config.ClientID,
	}

	// Ensure required streams exist
	if err := transport.ensureStreams(); err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create streams: %w", err)
	}

	return transport, nil
}

// ensureStreams creates necessary JetStream streams if they don't exist
func (t *Transport) ensureStreams() error {
	// Create request stream
	requestStreamName := "HTTP_REQUESTS"
	_, err := t.js.StreamInfo(requestStreamName)
	if err != nil {
		// Stream doesn't exist, create it
		_, err = t.js.AddStream(&nats.StreamConfig{
			Name:      requestStreamName,
			Subjects:  []string{"http.requests"},
			Retention: nats.WorkQueuePolicy, // Messages deleted after ack
			MaxAge:    24 * time.Hour,
			MaxMsgs:   10000,
			Storage:   nats.FileStorage,
		})
		if err != nil {
			return fmt.Errorf("failed to create request stream: %w", err)
		}
	}

	// Create reply stream for this client
	replyStreamName := fmt.Sprintf("HTTP_REPLIES_%s", t.clientID)
	_, err = t.js.StreamInfo(replyStreamName)
	if err != nil {
		// Stream doesn't exist, create it
		_, err = t.js.AddStream(&nats.StreamConfig{
			Name:      replyStreamName,
			Subjects:  []string{fmt.Sprintf("http.replies.%s.>", t.clientID)},
			Retention: nats.LimitsPolicy,
			MaxAge:    1 * time.Hour, // TTL for replies
			MaxMsgs:   1000,
			Storage:   nats.FileStorage,
		})
		if err != nil {
			return fmt.Errorf("failed to create reply stream: %w", err)
		}
	}

	return nil
}

// Ensure Transport implements http.RoundTripper
var _ http.RoundTripper = (*Transport)(nil)

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Serialize HTTP request to wire format using proper HTTP protocol
	var buf bytes.Buffer
	err := req.Write(&buf)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize HTTP request: %w", err)
	}
	httpBytes := buf.Bytes()

	// Create opaque NATS message
	requestID := uuid.New().String()
	replySubject := fmt.Sprintf("http.replies.%s.%s", t.clientID, requestID)

	natsMsg := NATSMessage{
		RequestID: requestID,
		ReplyTo:   replySubject,
		HTTPBytes: httpBytes,
		IsRequest: true,
	}

	// Marshal to JSON
	reqData, err := json.Marshal(natsMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal NATS message: %w", err)
	}

	// Create ephemeral consumer for this specific reply
	consumerName := fmt.Sprintf("reply-%s", requestID)
	replyStreamName := fmt.Sprintf("HTTP_REPLIES_%s", t.clientID)

	// Create ephemeral consumer and subscribe in one step
	sub, err := t.js.PullSubscribe(replySubject, consumerName, nats.BindStream(replyStreamName))
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to reply consumer: %w", err)
	}
	defer sub.Unsubscribe()

	// Publish request to JetStream
	_, err = t.js.Publish("http.requests", reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}

	// Wait for response using pull consumer
	msgs, err := sub.Fetch(1, nats.MaxWait(t.config.Timeout))
	if err != nil {
		return nil, fmt.Errorf("timeout waiting for response: %w", err)
	}

	if len(msgs) == 0 {
		return nil, fmt.Errorf("no response received")
	}

	msg := msgs[0]
	msg.Ack() // Acknowledge the response message

	// Parse response
	var natsResp NATSMessage
	if err := json.Unmarshal(msg.Data, &natsResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Check for transport-level error
	if natsResp.Error != "" {
		return nil, fmt.Errorf("transport error: %s", natsResp.Error)
	}

	// Deserialize HTTP response from wire format
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(natsResp.HTTPBytes)), req)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize HTTP response: %w", err)
	}

	return resp, nil
}

func (t *Transport) Shutdown(ctx context.Context) error {
	// Simple shutdown for now
	t.nc.Close()
	return nil
}

func (t *Transport) ListenForRequests(handler func(*http.Request) (*http.Response, error)) error {
	// Create durable consumer for request processing
	consumerName := t.config.ConsumerGroup
	if consumerName == "" {
		consumerName = "default-agent"
	}

	// Create durable consumer manually
	_, err := t.js.AddConsumer("HTTP_REQUESTS", &nats.ConsumerConfig{
		Durable:    consumerName,
		AckPolicy:  nats.AckExplicitPolicy,
		MaxDeliver: 3,
		AckWait:    30 * time.Second,
	})
	if err != nil {
		// Consumer might already exist, that's ok
		if !strings.Contains(err.Error(), "consumer name already in use") {
			return fmt.Errorf("failed to create consumer: %w", err)
		}
	}

	// Subscribe to the consumer
	sub, err := t.js.PullSubscribe("", "", nats.Bind("HTTP_REQUESTS", consumerName))
	if err != nil {
		return fmt.Errorf("failed to subscribe to consumer: %w", err)
	}
	defer sub.Unsubscribe()

	// Process messages in a loop
	for {
		// Fetch messages (blocking with timeout)
		msgs, err := sub.Fetch(1, nats.MaxWait(10*time.Second))
		if err != nil {
			// Timeout is expected, continue
			if err == nats.ErrTimeout {
				continue
			}
			return fmt.Errorf("failed to fetch message: %w", err)
		}

		if len(msgs) == 0 {
			continue
		}

		msg := msgs[0]

		// Process the message
		if err := t.processRequest(msg, handler); err != nil {
			// Log error but don't stop processing
			// In production, you might want to use a proper logger
			fmt.Printf("Error processing request: %v\n", err)
			msg.Nak() // Negative acknowledge - will be retried
		} else {
			msg.Ack() // Acknowledge successful processing
		}
	}
}

func (t *Transport) processRequest(msg *nats.Msg, handler func(*http.Request) (*http.Response, error)) error {
	// Parse NATS message
	var natsReq NATSMessage
	if err := json.Unmarshal(msg.Data, &natsReq); err != nil {
		return fmt.Errorf("failed to unmarshal NATS message: %w", err)
	}

	// Deserialize HTTP request from wire format  
	httpReq, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(natsReq.HTTPBytes)))
	if err != nil {
		// Send error response
		return t.sendErrorResponse(natsReq.RequestID, natsReq.ReplyTo, fmt.Sprintf("failed to deserialize HTTP request: %v", err))
	}

	// Call the handler
	httpResp, err := handler(httpReq)
	if err != nil {
		// Send error response
		return t.sendErrorResponse(natsReq.RequestID, natsReq.ReplyTo, fmt.Sprintf("handler error: %v", err))
	}

	// Serialize HTTP response to wire format
	respBuf := &bytes.Buffer{}
	if err := httpResp.Write(respBuf); err != nil {
		httpResp.Body.Close()
		return t.sendErrorResponse(natsReq.RequestID, natsReq.ReplyTo, fmt.Sprintf("failed to serialize HTTP response: %v", err))
	}
	httpResp.Body.Close()

	// Create NATS response message
	natsResp := NATSMessage{
		RequestID: natsReq.RequestID,
		HTTPBytes: respBuf.Bytes(),
		IsRequest: false,
	}

	// Send response back via JetStream
	return t.sendResponse(natsReq.ReplyTo, natsResp)
}

func (t *Transport) sendErrorResponse(requestID, replyTo, errorMsg string) error {
	errorResp := NATSMessage{
		RequestID: requestID,
		Error:     errorMsg,
		IsRequest: false,
	}
	return t.sendResponse(replyTo, errorResp)
}

func (t *Transport) sendResponse(replyTo string, natsResp NATSMessage) error {
	// Marshal response
	respData, err := json.Marshal(natsResp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// Publish response to JetStream
	_, err = t.js.Publish(replyTo, respData)
	if err != nil {
		return fmt.Errorf("failed to publish response: %w", err)
	}

	return nil
}
