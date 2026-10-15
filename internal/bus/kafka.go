package bus

import (
	"context"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

// Publisher writes one Kafka record per outbox row. The key is the aggregate id.
type Publisher struct {
	writer *kafka.Writer
}

// NewPublisher connects to the broker list. An empty list is refused by the caller.
func NewPublisher(brokers []string) *Publisher {
	return &Publisher{writer: &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Balancer:               &kafka.Hash{},
		AllowAutoTopicCreation: true,
		RequiredAcks:           kafka.RequireOne,
		BatchTimeout:           20 * time.Millisecond,
	}}
}

// Publish sends one record.
func (p *Publisher) Publish(ctx context.Context, topic, key string, payload []byte) error {
	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: payload,
	})
}

// Close flushes the writer.
func (p *Publisher) Close() error {
	return p.writer.Close()
}

// Brokers splits KAFKA_BROKERS.
func Brokers(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// EnsureTopics creates the worker topics before readers attach.
// A topic that already exists is left in place.
func EnsureTopics(ctx context.Context, brokers, topics []string) error {
	if len(brokers) == 0 {
		return nil
	}
	conn, err := kafka.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, topic := range topics {
		err := conn.CreateTopics(kafka.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1})
		if err != nil && !topicExists(err) {
			return err
		}
	}
	return nil
}

func topicExists(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "already exists") || strings.Contains(text, "topic_already_exists")
}
