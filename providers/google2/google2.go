package google2

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"cloud.google.com/go/pubsub/v2"
	pubsubpb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	ps "github.com/lileio/pubsub/v2"
	"github.com/segmentio/ksuid"
	"golang.org/x/sync/semaphore"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sirupsen/logrus"
)

var mutex = &sync.Mutex{}

// GoogleCloud2 provides google cloud pubsub using the latest v2 client library
type GoogleCloud2 struct {
	client      *pubsub.Client
	projectID   string
	publishers  map[string]*pubsub.Publisher
	subscribers map[string]*pubsub.Subscriber
	cancels     map[string]context.CancelFunc
	shutdown    bool
	mu          sync.RWMutex
}

// NewGoogleCloud2 creates a new GoogleCloud2 instance for a project
func NewGoogleCloud2(projectID string) (*GoogleCloud2, error) {
	// Try to use as many connections as possible. Use the same maximum default as Google's library
	numConns := runtime.GOMAXPROCS(0)
	if numConns > 4 {
		numConns = 4
	}

	// Check if we're using the Pub/Sub emulator
	var opts []option.ClientOption
	if os.Getenv("PUBSUB_EMULATOR_HOST") != "" {
		// When using the emulator, skip authentication and use insecure connections
		opts = append(opts, option.WithoutAuthentication())
		opts = append(opts, option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
	}
	opts = append(opts, option.WithGRPCConnectionPool(numConns))

	ctx := context.Background()
	c, err := pubsub.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub client: %w", err)
	}

	return &GoogleCloud2{
		projectID:   projectID,
		client:      c,
		publishers:  make(map[string]*pubsub.Publisher),
		subscribers: make(map[string]*pubsub.Subscriber),
		cancels:     make(map[string]context.CancelFunc),
		shutdown:    false,
	}, nil
}

// Publish implements Publish
func (g *GoogleCloud2) Publish(ctx context.Context, topic string, m *ps.Msg) error {
	topicName := fmt.Sprintf("projects/%s/topics/%s", g.projectID, topic)

	// Ensure topic exists
	err := g.ensureTopic(ctx, topic)
	if err != nil {
		return err
	}

	// Get or create publisher
	publisher := g.getPublisher(topicName)

	logrus.WithContext(ctx).Debug("Google Pubsub: Publishing")
	res := publisher.Publish(ctx, &pubsub.Message{
		Data:       m.Data,
		Attributes: m.Metadata,
	})

	_, err = res.Get(ctx)
	if err != nil {
		logrus.WithContext(ctx).Errorf("publish get failed: %v", err)
	} else {
		logrus.WithContext(ctx).Debug("Google Pubsub: Publish confirmed")
	}

	return err
}

// Subscribe implements Subscribe
func (g *GoogleCloud2) Subscribe(opts ps.HandlerOptions, h ps.MsgHandler) {
	go func() {
		subName := opts.ServiceName + "." + opts.Name + "--" + opts.Topic
		if opts.Unique {
			subName = subName + "-uniq-" + ksuid.New().String()
		}

		ctx := context.Background()
		topicName := fmt.Sprintf("projects/%s/topics/%s", g.projectID, opts.Topic)
		subscriptionName := fmt.Sprintf("projects/%s/subscriptions/%s", g.projectID, subName)

		// Ensure topic exists
		err := g.ensureTopic(ctx, opts.Topic)
		if err != nil {
			logrus.Panicf("Can't ensure topic %s: %s", opts.Topic, err.Error())
		}

		// Check if subscription exists and create if needed
		err = g.ensureSubscription(ctx, subscriptionName, topicName, opts.Deadline)
		if err != nil {
			logrus.Panicf("Can't ensure subscription %s: %s", subName, err.Error())
		}

		logrus.Infof("Subscribing to topic %s with subscription %s", opts.Topic, subName)

		// Set concurrency if not specified
		concurrency := opts.Concurrency
		if concurrency == 0 {
			concurrency = 10
		}

		// Get or create subscriber
		subscriber := g.getSubscriber(subscriptionName)

		// Configure subscriber settings
		subscriber.ReceiveSettings.MaxOutstandingMessages = concurrency * 2
		subscriber.ReceiveSettings.MaxOutstandingBytes = 10 * 1024 * 1024 // 10MB

		// Use semaphore to control concurrency
		sem := semaphore.NewWeighted(int64(concurrency))

		// Create context for this subscription
		subCtx, cancel := context.WithCancel(context.Background())
		g.mu.Lock()
		g.cancels[subName] = cancel
		g.mu.Unlock()

		// Start receiving messages
		err = subscriber.Receive(subCtx, func(ctx context.Context, msg *pubsub.Message) {
			// Acquire semaphore to control concurrency
			if err := sem.Acquire(ctx, 1); err != nil {
				logrus.Errorf("Failed to acquire semaphore: %v", err)
				msg.Nack()
				return
			}

			// Process message synchronously (Receive already handles concurrency)
			// Release semaphore when done
			defer sem.Release(1)

			// Create a context with deadline for this message
			msgCtx, cancel := context.WithTimeout(ctx, opts.Deadline)
			defer cancel()

			// Convert pubsub.Message to ps.Msg
			publishTime := msg.PublishTime
			psMsg := ps.Msg{
				ID:          msg.ID,
				Metadata:    msg.Attributes,
				Data:        msg.Data,
				PublishTime: &publishTime,
				Ack: func() {
					msg.Ack()
				},
				Nack: func() {
					msg.Nack()
				},
			}

			// Call the handler
			err := h(msgCtx, psMsg)
			if err != nil {
				psMsg.Nack()
				logrus.WithContext(msgCtx).Errorf("Handler error for message %s: %v", msg.ID, err)
				return
			}

			// Auto-ack if enabled
			if opts.AutoAck {
				psMsg.Ack()
			}
		})

		// Handle shutdown
		if opts.Unique && g.shutdown {
			err = g.client.SubscriptionAdminClient.DeleteSubscription(ctx, &pubsubpb.DeleteSubscriptionRequest{
				Subscription: subscriptionName,
			})
			if err != nil {
				logrus.Errorf("Failed to delete unique subscription %s: %v", subName, err)
			}
		}

		if err != nil && err != context.Canceled {
			logrus.Errorf("Subscription receive error for %s: %v", subName, err)
		}
	}()
}

// Shutdown shuts down all subscribers gracefully
func (g *GoogleCloud2) Shutdown() {
	g.mu.Lock()
	g.shutdown = true
	cancels := make(map[string]context.CancelFunc)
	for k, v := range g.cancels {
		cancels[k] = v
	}
	g.mu.Unlock()

	var wg sync.WaitGroup
	for k, cancel := range cancels {
		wg.Add(1)
		logrus.Infof("Shutting down subscription %s", k)
		go func(c context.CancelFunc) {
			c()
			wg.Done()
		}(cancel)
	}
	wg.Wait()

	// Stop all publishers
	g.mu.Lock()
	for _, publisher := range g.publishers {
		publisher.Stop()
	}
	g.mu.Unlock()

	// Close the client
	if g.client != nil {
		g.client.Close()
	}
}

func (g *GoogleCloud2) ensureTopic(ctx context.Context, name string) error {
	topicName := fmt.Sprintf("projects/%s/topics/%s", g.projectID, name)

	// Check if topic exists
	_, err := g.client.TopicAdminClient.GetTopic(ctx, &pubsubpb.GetTopicRequest{
		Topic: topicName,
	})
	if err == nil {
		// Topic exists
		return nil
	}

	// Topic doesn't exist, create it
	_, err = g.client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
		Name: topicName,
	})
	if err != nil {
		return fmt.Errorf("failed to create topic: %w", err)
	}

	return nil
}

func (g *GoogleCloud2) ensureSubscription(ctx context.Context, subscriptionName, topicName string, ackDeadline time.Duration) error {
	// Check if subscription exists
	_, err := g.client.SubscriptionAdminClient.GetSubscription(ctx, &pubsubpb.GetSubscriptionRequest{
		Subscription: subscriptionName,
	})
	if err == nil {
		// Subscription exists
		return nil
	}

	// Subscription doesn't exist, create it
	ackDeadlineSeconds := int32(ackDeadline.Seconds())
	if ackDeadlineSeconds < 10 {
		ackDeadlineSeconds = 10 // Minimum ack deadline
	}
	if ackDeadlineSeconds > 600 {
		ackDeadlineSeconds = 600 // Maximum ack deadline
	}

	_, err = g.client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:               subscriptionName,
		Topic:              topicName,
		AckDeadlineSeconds: ackDeadlineSeconds,
	})
	if err != nil {
		return fmt.Errorf("failed to create subscription: %w", err)
	}

	return nil
}

func (g *GoogleCloud2) getPublisher(topicName string) *pubsub.Publisher {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.publishers[topicName] != nil {
		return g.publishers[topicName]
	}

	publisher := g.client.Publisher(topicName)
	g.publishers[topicName] = publisher
	return publisher
}

func (g *GoogleCloud2) getSubscriber(subscriptionName string) *pubsub.Subscriber {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.subscribers[subscriptionName] != nil {
		return g.subscribers[subscriptionName]
	}

	subscriber := g.client.Subscriber(subscriptionName)
	g.subscribers[subscriptionName] = subscriber
	return subscriber
}
