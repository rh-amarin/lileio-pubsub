package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/lileio/pubsub/v2"
	"github.com/lileio/pubsub/v2/middleware/defaults"
	"github.com/lileio/pubsub/v2/providers/rabbitmq"
)

const HelloTopic = "hello.topic"

type Message struct {
	ID        string    `json:"id"`
	Data      string    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
	Random    int       `json:"random"`
}

func main() {
	rabbitmqURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@127.0.0.1:5672/")
	topicName := getEnv("RABBITMQ_TOPIC", HelloTopic)

	log.Printf("Starting publisher...")
	log.Printf("RabbitMQ URL: %s", rabbitmqURL)
	log.Printf("Topic: %s", topicName)

	rmq, err := rabbitmq.NewRabbitMQ(rabbitmqURL)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ at %s: %v\nMake sure RabbitMQ is running: docker run -d --name rabbitmq -p 5672:5672 -p 15672:15672 rabbitmq:3-management", rabbitmqURL, err)
	}

	pubsub.SetClient(&pubsub.Client{
		ServiceName: "publisher-service",
		Provider:    rmq,
		Middleware:  defaults.Middleware,
	})

	log.Printf("Publisher initialized successfully")

	// Seed random number generator
	rand.Seed(time.Now().UnixNano())

	ctx := context.Background()

	// Publish messages every second
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	counter := 0
	for range ticker.C {
		counter++

		msg := Message{
			ID:        fmt.Sprintf("msg-%d", counter),
			Data:      fmt.Sprintf("Random message #%d", counter),
			Timestamp: time.Now(),
			Random:    rand.Intn(1000),
		}

		data, err := json.Marshal(msg)
		if err != nil {
			log.Printf("Failed to marshal message: %v", err)
			continue
		}

		log.Printf("Publishing message: %s", string(data))

		r := pubsub.PublishJSON(ctx, topicName, msg)
		if r.Err != nil {
			log.Printf("Failed to publish message: %v", r.Err)
		} else {
			<-r.Ready
			log.Printf("Successfully published message #%d", counter)
		}
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
