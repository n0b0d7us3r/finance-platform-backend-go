package outbox

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"finance-platform/go/internal/event"
)

const schema = `
CREATE TABLE IF NOT EXISTS outbox (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id CHAR(36) NOT NULL,
  topic VARCHAR(64) NOT NULL,
  aggregate_id VARCHAR(64) NOT NULL,
  payload JSON NOT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  published_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY outbox_event_id (event_id),
  KEY outbox_unpublished (published_at, id)
)`

const deliverySchema = `
CREATE TABLE IF NOT EXISTS event_deliveries (
  event_id CHAR(36) NOT NULL,
  topic VARCHAR(64) NOT NULL,
  body JSON NOT NULL,
  meta JSON NULL,
  status VARCHAR(16) NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMP(6) NULL,
  created_at TIMESTAMP(6) NOT NULL,
  updated_at TIMESTAMP(6) NOT NULL,
  PRIMARY KEY (event_id),
  KEY event_deliveries_due (status, next_attempt_at)
)`

const idempotencySchema = `
CREATE TABLE IF NOT EXISTS app_request_keys (
  scope_key VARCHAR(191) NOT NULL,
  idem_key VARCHAR(191) NOT NULL,
  created_at TIMESTAMP(6) NOT NULL,
  PRIMARY KEY (scope_key, idem_key)
)`

// Ensure creates the outbox tables. They belong to this process, not the Laravel migrator.
func Ensure(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, deliverySchema); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, idempotencySchema)
	return err
}

// Insert writes one event in the caller's transaction.
func Insert(ctx context.Context, tx *sql.Tx, id, topic, aggregateID string, body any, now time.Time) error {
	encoded, err := marshal(body)
	if err != nil {
		return err
	}
	payload, err := marshal(event.Message{
		EventID:     id,
		Topic:       topic,
		AggregateID: aggregateID,
		Body:        encoded,
	})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox (event_id, topic, aggregate_id, payload, created_at)
		VALUES (?, ?, ?, ?, ?)`, id, topic, aggregateID, payload, now)
	return err
}

func marshal(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Row is one unpublished outbox record.
type Row struct {
	ID          int64
	Topic       string
	AggregateID string
	Payload     []byte
}

// Claim locks a batch of unpublished rows. publish runs before the rows are marked.
// A failure rolls the marks back; a second publish of the same event is safe.
func Claim(ctx context.Context, db *sql.DB, now time.Time, publish func(topic, key string, payload []byte) error) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, topic, aggregate_id, payload
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY id
		LIMIT 16
		FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return 0, err
	}
	var batch []Row
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.ID, &row.Topic, &row.AggregateID, &row.Payload); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, row := range batch {
		if err := publish(row.Topic, row.AggregateID, row.Payload); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE outbox SET published_at = ? WHERE id = ?`, now, row.ID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(batch), nil
}
