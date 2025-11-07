# LileIO PubSub GCP Emulator Test

This project demonstrates the usage of the [lileio/pubsub](https://github.com/lileio/pubsub/) library with Google Cloud PubSub emulator.

## Architecture

- **GCP PubSub Emulator**: Runs locally to simulate Google Cloud PubSub
- **Publisher**: Publishes random messages to a topic every second
- **Subscriber 1 & 2**: Two instances consuming from the same subscription (load balanced)

## Project Structure

```
.
├── docker-compose.yml       # Docker compose configuration
├── init-pubsub.sh          # Script to initialize topics and subscriptions
├── Dockerfile.publisher    # Publisher container definition
├── Dockerfile.subscriber   # Subscriber container definition
├── publisher/
│   └── main.go            # Publisher application
├── subscriber/
│   └── main.go            # Subscriber application
├── go.mod                 # Go module definition
└── go.sum                 # Go dependencies
```

## Prerequisites

- Docker and Docker Compose
- Go 1.23+ (for local development)

## Running the Application

### Using Docker Compose

1. Start all services:
```bash
docker-compose up --build
```

2. To run in detached mode:
```bash
docker-compose up -d --build
```

3. View logs:
```bash
# All services
docker-compose logs -f

# Specific service
docker-compose logs -f publisher
docker-compose logs -f subscriber-1
docker-compose logs -f subscriber-2
```

4. Stop all services:
```bash
docker-compose down
```

### Local Development

1. Start the PubSub emulator:
```bash
gcloud beta emulators pubsub start --host-port=localhost:8085
```

2. In another terminal, set the emulator host:
```bash
export PUBSUB_EMULATOR_HOST=localhost:8085
export PUBSUB_PROJECT_ID=test-project
```

3. Create topic and subscription:
```bash
gcloud pubsub topics create test-topic --project=test-project
gcloud pubsub subscriptions create test-subscription --topic=test-topic --project=test-project
```

4. Run the publisher:
```bash
cd publisher
go run main.go
```

5. Run subscribers (in separate terminals):
```bash
# Terminal 1
cd subscriber
SUBSCRIBER_NAME=subscriber-1 go run main.go

# Terminal 2
cd subscriber
SUBSCRIBER_NAME=subscriber-2 go run main.go
```

## How It Works

1. **Publisher** generates random messages every second with:
   - Unique ID
   - Random data
   - Timestamp
   - Random number (0-999)

2. **Subscribers** (2 instances):
   - Both subscribe to the same subscription
   - Messages are load-balanced between them
   - Each message is processed by only one subscriber instance
   - Shows processing time from message creation

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PUBSUB_EMULATOR_HOST` | - | PubSub emulator address |
| `PUBSUB_PROJECT_ID` | `test-project` | GCP project ID |
| `PUBSUB_TOPIC` | `test-topic` | Topic name |
| `PUBSUB_SUBSCRIPTION` | `test-subscription` | Subscription name |
| `SUBSCRIBER_NAME` | `subscriber` | Subscriber instance name |

## Monitoring

Watch the logs to see:
- Publisher sending messages every second
- Messages being distributed between subscriber-1 and subscriber-2
- Processing latency for each message

Example output:
```
publisher      | Publishing message: {"id":"msg-42","data":"Random message #42","timestamp":"...","random":567}
subscriber-1   | [subscriber-1] Received message: ID=msg-42, Data=Random message #42, Random=567, ProcessingTime=15ms
subscriber-2   | [subscriber-2] Received message: ID=msg-43, Data=Random message #43, Random=234, ProcessingTime=12ms
```

## Cleanup

```bash
docker-compose down -v
```

This removes all containers and volumes.
