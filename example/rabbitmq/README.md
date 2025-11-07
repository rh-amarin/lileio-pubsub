# LileIO PubSub RabbitMQ Example

This project demonstrates the usage of the [lileio/pubsub](https://github.com/lileio/pubsub/) library with RabbitMQ.

## Architecture

- **RabbitMQ**: Message broker running in Docker
- **Publisher**: Publishes random messages to a topic every second
- **Subscriber 1 & 2**: Two instances consuming from the same queue (load balanced)

## Project Structure

```
.
├── docker-compose.yml       # Docker compose configuration
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

1. Start RabbitMQ:
```bash
docker run -d --name rabbitmq -p 5672:5672 -p 15672:15672 rabbitmq:3-management
```

2. Access RabbitMQ Management UI:
```
http://localhost:15672
Username: guest
Password: guest
```

3. Run the publisher:
```bash
cd publisher
go run main.go
```

4. Run subscribers (in separate terminals):
```bash
# Terminal 1
cd subscriber
SUBSCRIBER_NAME=subscriber-1 go run main.go

# Terminal 2
cd subscriber
SUBSCRIBER_NAME=subscriber-2 go run main.go
```

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `RABBITMQ_URL` | `amqp://guest:guest@127.0.0.1:5672/` | RabbitMQ connection URL |
| `RABBITMQ_TOPIC` | `hello.topic` | Topic name |
| `SUBSCRIBER_NAME` | `subscriber` | Subscriber instance name |

## Monitoring

- RabbitMQ Management UI: http://localhost:15672
- Watch the logs to see messages being published and consumed
- Messages are distributed between subscriber-1 and subscriber-2

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


