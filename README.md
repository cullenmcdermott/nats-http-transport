# NATS HTTP Transport

A Go library that implements `http.RoundTripper` to route HTTP requests over NATS JetStream instead of traditional TCP connections. This enables HTTP communication across distributed systems using NATS as the transport layer.

## Architecture

```
┌─────────────┐    NATS JetStream    ┌─────────────┐    Traditional HTTP   ┌─────────────┐
│   Client    │ ───────────────────► │    Agent    │ ────────────────────► │   Server    │
│             │                      │             │                       │             │
│ HTTP Client │ ◄─────────────────── │ NATS Bridge │ ◄──────────────────── │ HTTP Server │
└─────────────┘                      └─────────────┘                       └─────────────┘
```

## Components

### Client Transport
- Drop-in replacement for `http.DefaultTransport`
- Serializes HTTP requests to NATS JetStream
- Handles response correlation and timeouts

### Agent (NATS-to-HTTP Bridge)
- Consumes HTTP requests from NATS JetStream
- Forwards requests to real HTTP servers
- Returns responses via NATS
- Supports horizontal scaling with consumer groups

### NATS JetStream Streams
- **Request Stream**: `HTTP_REQUESTS` - Durable storage for HTTP requests
- **Reply Streams**: `HTTP_REPLIES_{CLIENT_ID}` - Per-client response streams

## Usage

### Client Side

```go
import "github.com/cullenmcdermott/nats-http-transport/pkg/transport"

// Create NATS HTTP transport
natsTransport, err := transport.NewTransport(transport.Config{
    NATSURLs: []string{"nats://localhost:4222"},
    Timeout:  30 * time.Second,
})
defer natsTransport.Shutdown(context.Background())

// Use with standard HTTP client
client := &http.Client{Transport: natsTransport}
resp, err := client.Get("http://my-service/api")
```

### Agent Side

```go
// Create agent transport
agent, err := transport.NewTransport(transport.Config{
    NATSURLs:      []string{"nats://localhost:4222"},
    ConsumerGroup: "my-agents",
})

// Forward NATS requests to real HTTP servers
realClient := &http.Client{Transport: http.DefaultTransport}

agent.ListenForRequests(func(req *http.Request) (*http.Response, error) {
    return realClient.Do(req)
})
```
