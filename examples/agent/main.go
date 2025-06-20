package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/cullenmcdermott/nats-http-transport/pkg/transport"
)

type PingMessage struct {
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Timestamp string `json:"timestamp"`
}

type PongMessage struct {
	Message     string `json:"message"`
	RequestID   string `json:"request_id"`
	OriginalMsg string `json:"original_message"`
	ProcessedAt string `json:"processed_at"`
}

func main() {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://nats:4222"
	}

	log.Printf("Connecting to NATS at %s", natsURL)

	// Create NATS HTTP transport for agent
	agent, err := transport.NewTransport(transport.Config{
		NATSURLs:      []string{natsURL},
		ConsumerGroup: "pong-agents",
		Timeout:       10 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create NATS transport: %v", err)
	}
	defer agent.Shutdown(context.Background())

	log.Println("🚀 Agent started, listening for ping requests...")

	// Process requests from NATS
	err = agent.ListenForRequests(handlePingRequest)
	if err != nil {
		log.Fatalf("Agent failed: %v", err)
	}
}

func handlePingRequest(req *http.Request) (*http.Response, error) {
	requestID := req.Header.Get("X-Request-ID")
	
	log.Printf("🏓 Received ping request (ID: %s, Method: %s, URL: %s)", 
		requestID, req.Method, req.URL.Path)

	// Read request body
	body, err := io.ReadAll(req.Body)
	if err != nil {
		log.Printf("❌ Failed to read request body (ID: %s): %v", requestID, err)
		return createErrorResponse(500, "Failed to read request body"), nil
	}

	// Handle different paths
	switch req.URL.Path {
	case "/ping":
		return handlePing(body, requestID)
	case "/health":
		return handleHealth(requestID)
	default:
		log.Printf("❌ Unknown path (ID: %s): %s", requestID, req.URL.Path)
		return createErrorResponse(404, "Not Found"), nil
	}
}

func handlePing(body []byte, requestID string) (*http.Response, error) {
	// Parse ping message
	var ping PingMessage
	if err := json.Unmarshal(body, &ping); err != nil {
		log.Printf("❌ Invalid ping JSON (ID: %s): %v", requestID, err)
		return createErrorResponse(400, "Invalid JSON"), nil
	}

	log.Printf("📨 Processing ping (ID: %s): %s", requestID, ping.Message)

	// Add random jitter between 10-500ms to simulate processing time
	jitter := time.Duration(10+rand.Intn(491)) * time.Millisecond
	log.Printf("⏱️ Adding %v processing jitter (ID: %s)", jitter, requestID)
	time.Sleep(jitter)

	// Create pong response
	pong := PongMessage{
		Message:     "pong",
		RequestID:   ping.RequestID,
		OriginalMsg: ping.Message,
		ProcessedAt: time.Now().Format(time.RFC3339),
	}

	pongData, err := json.Marshal(pong)
	if err != nil {
		log.Printf("❌ Failed to marshal pong (ID: %s): %v", requestID, err)
		return createErrorResponse(500, "Failed to create response"), nil
	}

	log.Printf("📤 Sending pong (ID: %s)", requestID)

	return &http.Response{
		StatusCode:    200,
		Status:        "200 OK",
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(pongData)),
		ContentLength: int64(len(pongData)),
		ProtoMajor:    1,
		ProtoMinor:    1,
	}, nil
}

func handleHealth(requestID string) (*http.Response, error) {
	log.Printf("💚 Health check (ID: %s)", requestID)
	
	healthMsg := `{"status": "healthy", "service": "pong-agent"}`
	
	return &http.Response{
		StatusCode:    200,
		Status:        "200 OK",
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader([]byte(healthMsg))),
		ContentLength: int64(len(healthMsg)),
		ProtoMajor:    1,
		ProtoMinor:    1,
	}, nil
}

func createErrorResponse(statusCode int, message string) *http.Response {
	errorMsg := fmt.Sprintf(`{"error": "%s"}`, message)
	
	return &http.Response{
		StatusCode:    statusCode,
		Status:        fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader([]byte(errorMsg))),
		ContentLength: int64(len(errorMsg)),
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
}