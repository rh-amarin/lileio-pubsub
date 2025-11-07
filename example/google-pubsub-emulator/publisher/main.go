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
	"github.com/lileio/pubsub/v2/providers/google"
)

type Message struct {
	ID        string    `json:"id"`
	Data      string    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
	Random    int       `json:"random"`
}

func main() {
	projectID := getEnv("PUBSUB_PROJECT_ID", "test-project")
	topicName := getEnv("PUBSUB_TOPIC", "test-topic")
	emulatorHost := getEnv("PUBSUB_EMULATOR_HOST", "")

	log.Printf("Starting publisher...")
	log.Printf("Project ID: %s", projectID)
	log.Printf("Topic: %s", topicName)
	log.Printf("Emulator Host: %s", emulatorHost)

	ctx := context.Background()

	// Create Google PubSub provider
	provider, err := google.NewGoogleCloud(projectID)

	// Initialize the provider
	if err != nil {
		log.Fatalf("Failed to setup provider: %v", err)
	}

	client := pubsub.Client{
		ServiceName: "publisher-service",
		Provider:    provider,
		Middleware:  []pubsub.Middleware{},
	}
	// Create publisher
	pubsub.SetClient(&client)

	log.Printf("Publisher initialized successfully")

	// Seed random number generator
	rand.Seed(time.Now().UnixNano())

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

		result := pubsub.PublishJSON(ctx, topicName, data)
		<-result.Ready
		if result.Err != nil {
			log.Printf("Failed to publish message: %v", result.Err)
		} else {
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
