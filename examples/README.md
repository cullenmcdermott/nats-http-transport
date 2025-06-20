# NATS HTTP Transport Examples

This directory contains example implementations demonstrating the NATS HTTP transport library with a "ping-pong" demonstration.

## Architecture

```
┌─────────────┐    NATS JetStream     ┌─────────────┐
│   Client    │ ───────────────────► │    Agent    │
│             │                      │             │
│ Ping Sender │ ◄─────────────────── │ Pong Server │
└─────────────┘                      └─────────────┘
       │                                     │
       │              ┌─────────────┐        │
       └──────────────►│    NATS     │◄───────┘
                       │  JetStream  │
                       └─────────────┘
```

## Components

### Client (`examples/client/`)
- Sends ping messages every 3 seconds with unique request IDs
- Uses NATS HTTP transport as drop-in replacement for HTTP
- Cannot communicate directly with agent (network isolation)

### Agent (`examples/agent/`)
- Listens for HTTP requests via NATS JetStream
- Responds with pong messages containing original request data
- Supports `/ping` and `/health` endpoints

### Network Isolation
- Client and agent are on separate Docker networks
- Only NATS server is accessible from both networks
- Forces all communication through NATS JetStream

## Running the Example

```bash
# Start all services
docker-compose up --build

# View logs from specific services
docker-compose logs -f client
docker-compose logs -f agent
docker-compose logs -f nats

# Stop all services
docker-compose down
```

## Expected Output

**Client logs:**
```
🏓 Sending ping (ID: a1b2c3d4)
🏓 Received pong (ID: a1b2c3d4, Status: 200): {"message":"pong","request_id":"a1b2c3d4",...}
```

**Agent logs:**
```
🏓 Received ping request (ID: a1b2c3d4, Method: POST, URL: /ping)
📨 Processing ping (ID: a1b2c3d4): ping
📤 Sending pong (ID: a1b2c3d4)
```

## Message Flow

1. **Client** generates unique request ID and ping message
2. **Client** sends HTTP POST via NATS transport to `http://pong-service/ping`
3. **NATS JetStream** stores request in `HTTP_REQUESTS` stream
4. **Agent** consumes request from JetStream via consumer group
5. **Agent** processes ping and creates pong response
6. **Agent** sends response via NATS to client's reply stream
7. **Client** receives pong response with original request ID

## Monitoring NATS

Access NATS monitoring at http://localhost:8222

```bash
# View stream info
docker-compose exec nats nats stream info HTTP_REQUESTS

# View consumer info
docker-compose exec nats nats consumer info HTTP_REQUESTS pong-agents
```