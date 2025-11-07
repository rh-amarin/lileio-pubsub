package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lileio/pubsub/v2"
	"github.com/lileio/pubsub/v2/providers/google2"
)

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
	topicName := getEnv("PUBSUB_TOPIC", "test-topic")
	subscriptionName := getEnv("PUBSUB_SUBSCRIPTION", "test-subscription")

	c.On(pubsub.HandlerOptions{
		Topic: topicName,
		Name:  subscriptionName,
		// ServiceName: fmt.Sprintf("subscriber-service-%s", s.subscriberName),
		ServiceName: fmt.Sprintf("test-subscriber"),
		Handler:     handleMessage,
		AutoAck:     true,
		JSON:        true,
		Concurrency: 1,
		Deadline:    10 * time.Second,
	})
}

// func (s *Subscriber) handleMessage(ctx context.Context, msg *Message, m *pubsub.Msg) error {
func handleMessage(ctx context.Context, payload *string, m *pubsub.Msg) error {
	decoded, err := base64.StdEncoding.DecodeString(*payload)
	if err != nil {
		log.Printf("failed to decode base64 payload")
		return err
	}
	var msg Message
	if err := json.Unmarshal(decoded, &msg); err != nil {
		log.Printf("failed to unmarshal decoded payload")
		return err
	}
	processingTime := time.Since(msg.Timestamp)
	log.Printf("[%s] Received message: ID=%s, Data=%s, Random=%d, ProcessingTime=%v",
		"subscriberName", msg.ID, msg.Data, msg.Random, processingTime)

	// Simulate some processing
	time.Sleep(100 * time.Millisecond)

	return nil
}

func main() {
	projectID := getEnv("PUBSUB_PROJECT_ID", "test-project")
	topicName := getEnv("PUBSUB_TOPIC", "test-topic")
	subscriptionName := getEnv("PUBSUB_SUBSCRIPTION", "test-subscription")
	subscriberName := getEnv("SUBSCRIBER_NAME", "subscriber")
	emulatorHost := getEnv("PUBSUB_EMULATOR_HOST", "")

	log.Printf("Starting subscriber: %s", subscriberName)
	log.Printf("Project ID: %s", projectID)
	log.Printf("Topic: %s", topicName)
	log.Printf("Subscription: %s", subscriptionName)
	log.Printf("Emulator Host: %s", emulatorHost)

	// Create Google PubSub provider using google2 (latest client library)
	provider, err := google2.NewGoogleCloud2(projectID)
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}

	// Create and set the client
	client := pubsub.Client{
		ServiceName: fmt.Sprintf("test-service", subscriberName),
		Provider:    provider,
		Middleware:  []pubsub.Middleware{},
		// Middleware: defaults.Middleware,
	}
	pubsub.SetClient(&client)

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
