package store

import (
	"context"
	"database/sql"
	"errors"
)

// FindUserIDByAccessToken matches the statement in db/queries/users.sql.
const findUserIDByAccessToken = `
SELECT id
FROM users
WHERE api_access_token = ?
LIMIT 1
`

// ErrNotFound is a missing access token row.
var ErrNotFound = errors.New("user not found")

// FindUserIDByAccessToken returns the user id for an Access-Token.
func FindUserIDByAccessToken(ctx context.Context, db *sql.DB, token string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, findUserIDByAccessToken, token).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}
