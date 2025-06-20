package transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testServer starts an embedded NATS server with JetStream
func testServer(t *testing.T) (*server.Server, string) {
	opts := &server.Options{
		Port:      -1, // Random port
		JetStream: true,
	}
	
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	
	go ns.Start()
	require.True(t, ns.ReadyForConnections(2*time.Second))
	
	return ns, ns.ClientURL()
}

func TestNewTransport(t *testing.T) {
	ns, url := testServer(t)
	defer ns.Shutdown()
	
	config := Config{
		NATSURLs: []string{url},
		Timeout:  5 * time.Second,
	}
	
	transport, err := NewTransport(config)
	require.NoError(t, err)
	defer transport.Shutdown(context.Background())
	
	assert.NotEmpty(t, transport.clientID, "ClientID should be auto-generated")
	assert.Equal(t, 5*time.Second, transport.config.Timeout)
}

func TestTransportImplementsRoundTripper(t *testing.T) {
	var _ http.RoundTripper = (*Transport)(nil)
}

func TestConfigDefaults(t *testing.T) {
	ns, url := testServer(t)
	defer ns.Shutdown()
	
	config := Config{
		NATSURLs: []string{url},
		// No timeout specified
	}
	
	transport, err := NewTransport(config)
	require.NoError(t, err)
	defer transport.Shutdown(context.Background())
	
	assert.Equal(t, 30*time.Second, transport.config.Timeout, "Should default to 30s")
}

// Helper to create a simple echo handler
func echoHandler(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	
	
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     req.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(body)),
		ProtoMajor: 1,
		ProtoMinor: 1,
	}, nil
}

// Helper to create transport pair (client + agent)
func createTransportPair(t *testing.T, natsURL string) (*Transport, *Transport) {
	clientConfig := Config{
		NATSURLs: []string{natsURL},
		Timeout:  2 * time.Second,
	}
	
	agentConfig := Config{
		NATSURLs:      []string{natsURL},
		Timeout:       2 * time.Second,
		ConsumerGroup: "test-agents",
	}
	
	client, err := NewTransport(clientConfig)
	require.NoError(t, err)
	
	agent, err := NewTransport(agentConfig)
	require.NoError(t, err)
	
	return client, agent
}

func TestSimpleRequestResponse(t *testing.T) {
	ns, url := testServer(t)
	defer ns.Shutdown()
	
	client, agent := createTransportPair(t, url)
	defer client.Shutdown(context.Background())
	defer agent.Shutdown(context.Background())
	
	// Start agent in goroutine with error handling
	agentDone := make(chan error, 1)
	go func() {
		err := agent.ListenForRequests(echoHandler)
		agentDone <- err
	}()
	
	time.Sleep(100 * time.Millisecond) // Let agent start
	
	// Check if agent had an immediate error
	select {
	case err := <-agentDone:
		t.Fatalf("Agent failed to start: %v", err)
	default:
		// No immediate error, continue
	}
	
	// Create request
	body := []byte("hello")
	req, err := http.NewRequest("GET", "http://test.example/", bytes.NewReader(body))
	require.NoError(t, err)
	req.ContentLength = int64(len(body)) // Set Content-Length for proper HTTP serialization
	
	// Send via NATS transport
	resp, err := client.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	
	// Verify response
	assert.Equal(t, 200, resp.StatusCode)
	
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(respBody))
}

func TestHTTPHeadersPreserved(t *testing.T) {
	ns, url := testServer(t)
	defer ns.Shutdown()
	
	client, agent := createTransportPair(t, url)
	defer client.Shutdown(context.Background())
	defer agent.Shutdown(context.Background())
	
	// Start agent with echo handler
	go agent.ListenForRequests(echoHandler)
	time.Sleep(50 * time.Millisecond)
	
	// Create request with custom headers
	req, err := http.NewRequest("POST", "http://test.example/api", nil)
	require.NoError(t, err)
	req.Header.Set("X-Custom-Header", "test-value")
	req.Header.Set("Content-Type", "application/json")
	
	// Send request
	resp, err := client.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	
	// Verify headers were preserved
	assert.Equal(t, "test-value", resp.Header.Get("X-Custom-Header"))
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

func TestTimeoutWhenNoAgent(t *testing.T) {
	ns, url := testServer(t)
	defer ns.Shutdown()
	
	config := Config{
		NATSURLs: []string{url},
		Timeout:  100 * time.Millisecond, // Very short timeout
	}
	
	client, err := NewTransport(config)
	require.NoError(t, err)
	defer client.Shutdown(context.Background())
	
	// Create request
	req, err := http.NewRequest("GET", "http://test.example/", nil)
	require.NoError(t, err)
	
	// Should timeout since no agent is listening
	_, err = client.RoundTrip(req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestAgentErrorHandling(t *testing.T) {
	ns, url := testServer(t)
	defer ns.Shutdown()
	
	client, agent := createTransportPair(t, url)
	defer client.Shutdown(context.Background())
	defer agent.Shutdown(context.Background())
	
	// Start agent with error handler
	go agent.ListenForRequests(func(req *http.Request) (*http.Response, error) {
		return nil, assert.AnError // Return an error
	})
	time.Sleep(50 * time.Millisecond)
	
	// Create request
	req, err := http.NewRequest("GET", "http://test.example/", nil)
	require.NoError(t, err)
	
	// Should get transport error
	_, err = client.RoundTrip(req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "transport error")
}