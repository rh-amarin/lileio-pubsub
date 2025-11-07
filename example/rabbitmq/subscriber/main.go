package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
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

type Subscriber struct {
	subscriberName string
}

func (s *Subscriber) Setup(c *pubsub.Client) {
	topicName := getEnv("RABBITMQ_TOPIC", HelloTopic)

	c.On(pubsub.HandlerOptions{
		Topic:       topicName,
		Name:        "print-hello",
		ServiceName: fmt.Sprintf("subscriber-service-%s", s.subscriberName),
		Handler:     s.handleMessage,
		AutoAck:     true,
		JSON:        true,
	})
}

func (s *Subscriber) handleMessage(ctx context.Context, msg *Message, m *pubsub.Msg) error {
	processingTime := time.Since(msg.Timestamp)
	log.Printf("[%s] Received message: ID=%s, Data=%s, Random=%d, ProcessingTime=%v",
		s.subscriberName, msg.ID, msg.Data, msg.Random, processingTime)

	// Simulate some processing
	time.Sleep(100 * time.Millisecond)

	return nil
}

func main() {
	rabbitmqURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@127.0.0.1:5672/")
	topicName := getEnv("RABBITMQ_TOPIC", HelloTopic)
	subscriberName := getEnv("SUBSCRIBER_NAME", "subscriber")

	log.Printf("Starting subscriber: %s", subscriberName)
	log.Printf("RabbitMQ URL: %s", rabbitmqURL)
	log.Printf("Topic: %s", topicName)

	rmq, err := rabbitmq.NewRabbitMQ(rabbitmqURL)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ at %s: %v\nMake sure RabbitMQ is running: docker run -d --name rabbitmq -p 5672:5672 -p 15672:15672 rabbitmq:3-management", rabbitmqURL, err)
	}

	pubsub.SetClient(&pubsub.Client{
		ServiceName: fmt.Sprintf("subscriber-service-%s", subscriberName),
		Provider:    rmq,
		Middleware:  defaults.Middleware,
	})

	log.Printf("Subscriber %s initialized successfully", subscriberName)

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start subscriber in a goroutine
	go func() {
		log.Printf("Starting to consume messages...")
		pubsub.Subscribe(&Subscriber{subscriberName: subscriberName})
	}()

	log.Printf("Subscribing to queues..")

	// Wait for shutdown signal
	sig := <-sigChan
	log.Printf("Received signal %v, shutting down gracefully...", sig)
	pubsub.Shutdown()

	log.Printf("Subscriber %s stopped", subscriberName)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
