package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cullenmcdermott/nats-http-transport/pkg/transport"
	"github.com/google/uuid"
)

func main() {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://nats:4222"
	}

	log.Printf("Connecting to NATS at %s", natsURL)

	// Create NATS HTTP transport
	natsTransport, err := transport.NewTransport(transport.Config{
		NATSURLs: []string{natsURL},
		Timeout:  10 * time.Second,
		ClientID: "ping-client",
	})
	if err != nil {
		log.Fatalf("Failed to create NATS transport: %v", err)
	}
	defer natsTransport.Shutdown(context.Background())

	// Create HTTP client using NATS transport
	client := &http.Client{
		Transport: natsTransport,
		Timeout:   15 * time.Second,
	}

	log.Println("Starting ping-pong client...")

	// Send ping-pong requests every 3 seconds
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			requestID := uuid.New().String()[:8]
			
			if err := sendPing(client, requestID); err != nil {
				log.Printf("❌ Ping failed (ID: %s): %v", requestID, err)
			}
		}
	}
}

func sendPing(client *http.Client, requestID string) error {
	pingMessage := fmt.Sprintf(`{"message": "ping", "request_id": "%s", "timestamp": "%s"}`, 
		requestID, time.Now().Format(time.RFC3339))

	// Create HTTP POST request with ping message
	req, err := http.NewRequest("POST", "http://pong-service/ping", bytes.NewReader([]byte(pingMessage)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	req.ContentLength = int64(len(pingMessage))

	log.Printf("🏓 Sending ping (ID: %s)", requestID)

	// Send request via NATS
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	log.Printf("🏓 Received pong (ID: %s, Status: %d): %s", 
		requestID, resp.StatusCode, string(body))

	return nil
}