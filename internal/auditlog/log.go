package auditlog

import (
	"context"
	"encoding/json"

	goredis "github.com/redis/go-redis/v9"
)

const key = "security:actions"
const keep = 499

// Entry is one bulk security action. The list lives in its own Redis database.
type Entry struct {
	ID        string `json:"id"`
	UserID    int64  `json:"user_id"`
	UserName  string `json:"user_name"`
	UserEmail string `json:"user_email"`
	Action    string `json:"action"`
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
}

// Log is the security action history.
type Log interface {
	Append(ctx context.Context, entry Entry) error
	Recent(ctx context.Context, limit int) ([]Entry, error)
}

// RedisLog is a list on the persistent price Redis, database AUDIT_REDIS_DB.
type RedisLog struct {
	Client *goredis.Client
}

func (l RedisLog) Append(ctx context.Context, entry Entry) error {
	if l.Client == nil {
		return nil
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	pipe := l.Client.TxPipeline()
	pipe.LPush(ctx, key, raw)
	pipe.LTrim(ctx, key, 0, keep)
	_, err = pipe.Exec(ctx)
	return err
}

func (l RedisLog) Recent(ctx context.Context, limit int) ([]Entry, error) {
	if l.Client == nil {
		return []Entry{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	raw, err := l.Client.LRange(ctx, key, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	items := make([]Entry, 0, len(raw))
	for _, line := range raw {
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		items = append(items, entry)
	}
	return items, nil
}
