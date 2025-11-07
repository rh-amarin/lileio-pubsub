package rabbitmq

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	"github.com/lileio/logr"
	"github.com/lileio/pubsub/v2"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// RabbitMQ provides RabbitMQ publish and subscribe
type RabbitMQ struct {
	conn         *amqp.Connection
	channel      *amqp.Channel
	url          string
	topics       map[string]*amqp.Queue
	subscriptions map[string]*amqp.Channel
	shutdown     bool
	mu           sync.RWMutex
}

// NewRabbitMQ creates a new RabbitMQ connection
func NewRabbitMQ(url string) (*RabbitMQ, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect to RabbitMQ")
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, errors.Wrap(err, "failed to open channel")
	}

	return &RabbitMQ{
		conn:          conn,
		channel:       ch,
		url:           url,
		topics:        make(map[string]*amqp.Queue),
		subscriptions: make(map[string]*amqp.Channel),
		shutdown:      false,
	}, nil
}

// Publish implements Publish
func (r *RabbitMQ) Publish(ctx context.Context, topic string, m *pubsub.Msg) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.shutdown {
		return errors.New("RabbitMQ provider is shutdown")
	}

	w := &pubsub.MessageWrapper{
		Data:        m.Data,
		Metadata:    m.Metadata,
		PublishTime: ptypes.TimestampNow(),
	}

	b, err := proto.Marshal(w)
	if err != nil {
		return errors.Wrap(err, "failed to marshal message wrapper")
	}

	// Declare exchange if it doesn't exist
	err = r.channel.ExchangeDeclare(
		topic,    // exchange name (using topic name)
		"topic",  // type
		true,     // durable
		false,    // auto-deleted
		false,    // internal
		false,    // no-wait
		nil,      // arguments
	)
	if err != nil {
		return errors.Wrap(err, "failed to declare exchange")
	}

	// Publish message
	err = r.channel.PublishWithContext(
		ctx,
		topic,    // exchange
		"",       // routing key
		false,    // mandatory
		false,    // immediate
		amqp.Publishing{
			ContentType:  "application/protobuf",
			Body:         b,
			DeliveryMode: amqp.Persistent, // make message persistent
			Timestamp:    time.Now(),
			MessageId:    m.ID,
			Headers:      convertMetadataToHeaders(m.Metadata),
		},
	)

	if err != nil {
		logr.WithCtx(ctx).Error(errors.Wrap(err, "couldn't publish to RabbitMQ"))
		return err
	}

	logr.WithCtx(ctx).Debugf("RabbitMQ: Published to %s", topic)
	return nil
}

// Subscribe implements Subscribe for RabbitMQ
func (r *RabbitMQ) Subscribe(opts pubsub.HandlerOptions, h pubsub.MsgHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.shutdown {
		logrus.Errorf("RabbitMQ: Cannot subscribe, provider is shutdown")
		return
	}

	// Create a dedicated channel for this subscription
	ch, err := r.conn.Channel()
	if err != nil {
		logrus.Errorf("RabbitMQ: Failed to open channel for subscription: %v", err)
		return
	}

	queueName := fmt.Sprintf("%s--%s", opts.ServiceName, opts.Name)
	if opts.Unique {
		// For unique subscribers, use a temporary queue
		queueName = fmt.Sprintf("%s--%s--%d", opts.ServiceName, opts.Name, time.Now().UnixNano())
	}

	// Declare exchange
	err = ch.ExchangeDeclare(
		opts.Topic, // exchange name
		"topic",    // type
		true,       // durable
		false,      // auto-deleted
		false,      // internal
		false,      // no-wait
		nil,        // arguments
	)
	if err != nil {
		logrus.Errorf("RabbitMQ: Failed to declare exchange %s: %v", opts.Topic, err)
		ch.Close()
		return
	}

	// Declare queue
	queue, err := ch.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		amqp.Table{
			"x-message-ttl": int(opts.Deadline.Milliseconds()),
		},
	)
	if err != nil {
		logrus.Errorf("RabbitMQ: Failed to declare queue %s: %v", queueName, err)
		ch.Close()
		return
	}

	// Bind queue to exchange
	err = ch.QueueBind(
		queue.Name, // queue name
		"",         // routing key (empty means all messages)
		opts.Topic, // exchange
		false,      // no-wait
		nil,        // arguments
	)
	if err != nil {
		logrus.Errorf("RabbitMQ: Failed to bind queue %s to exchange %s: %v", queueName, opts.Topic, err)
		ch.Close()
		return
	}

	// Set QoS for concurrency control
	err = ch.Qos(
		opts.Concurrency, // prefetch count
		0,                // prefetch size
		false,            // global
	)
	if err != nil {
		logrus.Errorf("RabbitMQ: Failed to set QoS: %v", err)
		ch.Close()
		return
	}

	r.topics[opts.Topic] = &queue
	r.subscriptions[opts.Topic] = ch

	// Start consuming messages
	msgs, err := ch.Consume(
		queue.Name, // queue
		"",         // consumer tag
		opts.AutoAck, // auto-ack
		false,        // exclusive
		false,        // no-local
		false,        // no-wait
		nil,          // arguments
	)
	if err != nil {
		logrus.Errorf("RabbitMQ: Failed to register consumer for queue %s: %v", queueName, err)
		ch.Close()
		return
	}

	logrus.Infof("RabbitMQ: Subscribed to %s with queue %s", opts.Topic, queueName)

	// Process messages
	go func() {
		for d := range msgs {
			r.mu.RLock()
			shutdown := r.shutdown
			r.mu.RUnlock()
			if shutdown {
				return
			}

			var w pubsub.MessageWrapper
			err := proto.Unmarshal(d.Body, &w)
			if err != nil {
				logr.WithCtx(context.Background()).Errorf(
					"RabbitMQ: [WARNING] Couldn't unmarshal msg from topic %s, reason: %s",
					opts.Topic,
					err,
				)
				if !opts.AutoAck {
					d.Nack(false, false) // reject and don't requeue
				}
				continue
			}

			t, err := ptypes.Timestamp(w.PublishTime)
			if err != nil {
				logr.WithCtx(context.Background()).Errorf(
					"RabbitMQ: [WARNING] Couldn't unmarshal timestamp topic %s, reason: %s",
					opts.Topic,
					err,
				)
				if !opts.AutoAck {
					d.Nack(false, false)
				}
				continue
			}

			msgID := d.MessageId
			if msgID == "" {
				msgID = strconv.FormatUint(d.DeliveryTag, 10)
			}

			msg := pubsub.Msg{
				ID:          msgID,
				Metadata:    convertHeadersToMetadata(d.Headers),
				Data:        w.Data,
				PublishTime: &t,
				Ack: func() {
					if !opts.AutoAck {
						d.Ack(false)
					}
				},
				Nack: func() {
					if !opts.AutoAck {
						d.Nack(false, true) // requeue on nack
					}
				},
			}

			// Merge metadata from headers if available
			if len(w.Metadata) > 0 {
				if msg.Metadata == nil {
					msg.Metadata = make(map[string]string)
				}
				for k, v := range w.Metadata {
					msg.Metadata[k] = v
				}
			}

			err = h(context.Background(), msg)
			if err != nil {
				if !opts.AutoAck {
					msg.Nack()
				}
				continue
			}

			if opts.AutoAck {
				msg.Ack()
			}
		}
	}()
}

// Shutdown shuts down all subscribers gracefully
func (r *RabbitMQ) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.shutdown = true

	if r.channel != nil {
		r.channel.Close()
	}

	for topic, ch := range r.subscriptions {
		logrus.Infof("Shutting down sub for %s", topic)
		if ch != nil {
			ch.Close()
		}
	}

	if r.conn != nil {
		r.conn.Close()
	}
}

// convertMetadataToHeaders converts metadata map to AMQP headers
func convertMetadataToHeaders(metadata map[string]string) amqp.Table {
	if metadata == nil {
		return nil
	}
	headers := make(amqp.Table)
	for k, v := range metadata {
		headers[k] = v
	}
	return headers
}

// convertHeadersToMetadata converts AMQP headers to metadata map
func convertHeadersToMetadata(headers amqp.Table) map[string]string {
	if headers == nil {
		return make(map[string]string)
	}
	metadata := make(map[string]string)
	for k, v := range headers {
		if str, ok := v.(string); ok {
			metadata[k] = str
		}
	}
	return metadata
}

