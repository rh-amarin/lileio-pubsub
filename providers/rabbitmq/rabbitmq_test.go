package rabbitmq

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/lileio/pubsub/v2"
	"github.com/lileio/pubsub/v2/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRabbitMQPublishSubscribe(t *testing.T) {
	// Check if RabbitMQ is available
	rabbitmqURL := os.Getenv("RABBITMQ_URL")
	if rabbitmqURL == "" {
		rabbitmqURL = "amqp://guest:guest@127.0.0.1:5672/"
	}

	// Try to connect to RabbitMQ (use IPv4 explicitly)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:5672", 2*time.Second)
	if err != nil {
		t.Skipf("RabbitMQ is not available at localhost:5672, skipping test: %v", err)
		return
	}
	conn.Close()

	rmq, err := NewRabbitMQ(rabbitmqURL)
	require.NoError(t, err)
	require.NotNil(t, rmq)
	defer rmq.Shutdown()

	done := make(chan bool, 1)
	topic := "test_topic_" + time.Now().Format("20060102150405")

	opts := pubsub.HandlerOptions{
		Topic:       topic,
		Name:        "test_subscriber",
		ServiceName: "test_service",
		AutoAck:     false,
		Concurrency: 1,
		Deadline:    10 * time.Second,
	}

	// Subscribe
	rmq.Subscribe(opts, func(ctx context.Context, m pubsub.Msg) error {
		var ac test.Account
		err := proto.Unmarshal(m.Data, &ac)
		assert.NoError(t, err)
		assert.Equal(t, "Alex", ac.Name)
		m.Ack()
		done <- true
		return nil
	})

	// Wait a bit for subscription to be ready
	time.Sleep(500 * time.Millisecond)

	// Publish
	a := test.Account{Name: "Alex"}
	data, err := proto.Marshal(&a)
	require.NoError(t, err)

	msg := &pubsub.Msg{
		Data:     data,
		Metadata: map[string]string{"test": "metadata"},
	}

	err = rmq.Publish(context.Background(), topic, msg)
	assert.NoError(t, err)

	// Wait for message to be received
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Message not received within timeout")
	}
}

func TestRabbitMQPublishSubscribeJSON(t *testing.T) {
	// Check if RabbitMQ is available
	rabbitmqURL := os.Getenv("RABBITMQ_URL")
	if rabbitmqURL == "" {
		rabbitmqURL = "amqp://guest:guest@127.0.0.1:5672/"
	}

	// Try to connect to RabbitMQ (use IPv4 explicitly)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:5672", 2*time.Second)
	if err != nil {
		t.Skipf("RabbitMQ is not available at 127.0.0.1:5672, skipping test: %v", err)
		return
	}
	conn.Close()

	rmq, err := NewRabbitMQ(rabbitmqURL)
	require.NoError(t, err)
	require.NotNil(t, rmq)
	defer rmq.Shutdown()

	done := make(chan bool, 1)
	topic := "test_topic_json_" + time.Now().Format("20060102150405")

	type TestMessage struct {
		Greeting string `json:"greeting"`
		Name     string `json:"name"`
	}

	opts := pubsub.HandlerOptions{
		Topic:       topic,
		Name:        "test_subscriber_json",
		ServiceName: "test_service",
		AutoAck:     true,
		Concurrency: 1,
		Deadline:    10 * time.Second,
	}

	// Subscribe
	rmq.Subscribe(opts, func(ctx context.Context, m pubsub.Msg) error {
		// In a real scenario, you'd unmarshal JSON here
		// For this test, we just check that data was received
		assert.NotEmpty(t, m.Data)
		done <- true
		return nil
	})

	// Wait a bit for subscription to be ready
	time.Sleep(500 * time.Millisecond)

	// Publish JSON-like data
	msg := &pubsub.Msg{
		Data:     []byte(`{"greeting":"Hello","name":"World"}`),
		Metadata: map[string]string{"content-type": "application/json"},
	}

	err = rmq.Publish(context.Background(), topic, msg)
	assert.NoError(t, err)

	// Wait for message to be received
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Message not received within timeout")
	}
}

func TestRabbitMQShutdown(t *testing.T) {
	// Check if RabbitMQ is available
	rabbitmqURL := os.Getenv("RABBITMQ_URL")
	if rabbitmqURL == "" {
		rabbitmqURL = "amqp://guest:guest@127.0.0.1:5672/"
	}

	// Try to connect to RabbitMQ (use IPv4 explicitly)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:5672", 2*time.Second)
	if err != nil {
		t.Skipf("RabbitMQ is not available at 127.0.0.1:5672, skipping test: %v", err)
		return
	}
	conn.Close()

	rmq, err := NewRabbitMQ(rabbitmqURL)
	require.NoError(t, err)
	require.NotNil(t, rmq)

	// Subscribe to a topic
	opts := pubsub.HandlerOptions{
		Topic:       "test_shutdown",
		Name:        "test_subscriber",
		ServiceName: "test_service",
		AutoAck:     true,
	}

	rmq.Subscribe(opts, func(ctx context.Context, m pubsub.Msg) error {
		return nil
	})

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Shutdown should not panic
	assert.NotPanics(t, func() {
		rmq.Shutdown()
	})

	// Second shutdown should also not panic
	assert.NotPanics(t, func() {
		rmq.Shutdown()
	})
}

func TestRabbitMQMetadata(t *testing.T) {
	// Check if RabbitMQ is available
	rabbitmqURL := os.Getenv("RABBITMQ_URL")
	if rabbitmqURL == "" {
		rabbitmqURL = "amqp://guest:guest@127.0.0.1:5672/"
	}

	// Try to connect to RabbitMQ (use IPv4 explicitly)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:5672", 2*time.Second)
	if err != nil {
		t.Skipf("RabbitMQ is not available at 127.0.0.1:5672, skipping test: %v", err)
		return
	}
	conn.Close()

	rmq, err := NewRabbitMQ(rabbitmqURL)
	require.NoError(t, err)
	require.NotNil(t, rmq)
	defer rmq.Shutdown()

	done := make(chan bool, 1)
	topic := "test_metadata_" + time.Now().Format("20060102150405")

	opts := pubsub.HandlerOptions{
		Topic:       topic,
		Name:        "test_subscriber_metadata",
		ServiceName: "test_service",
		AutoAck:     true,
		Concurrency: 1,
		Deadline:    10 * time.Second,
	}

	expectedMetadata := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}

	// Subscribe
	rmq.Subscribe(opts, func(ctx context.Context, m pubsub.Msg) error {
		// Check that metadata is preserved
		assert.Equal(t, expectedMetadata["key1"], m.Metadata["key1"])
		assert.Equal(t, expectedMetadata["key2"], m.Metadata["key2"])
		done <- true
		return nil
	})

	// Wait a bit for subscription to be ready
	time.Sleep(500 * time.Millisecond)

	// Publish with metadata
	msg := &pubsub.Msg{
		Data:     []byte("test data"),
		Metadata: expectedMetadata,
	}

	err = rmq.Publish(context.Background(), topic, msg)
	assert.NoError(t, err)

	// Wait for message to be received
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Message not received within timeout")
	}
}
