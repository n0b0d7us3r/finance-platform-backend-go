package store

import (
	"context"
	"database/sql"
	"errors"
)

// EnsureUserRoles creates the user-to-role table and copies assignments
// that were stored as a polymorphic model type.
func EnsureUserRoles(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS user_roles (
			user_id BIGINT UNSIGNED NOT NULL,
			role_id BIGINT UNSIGNED NOT NULL,
			PRIMARY KEY (user_id, role_id),
			CONSTRAINT fk_user_roles_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
			CONSTRAINT fk_user_roles_role FOREIGN KEY (role_id) REFERENCES roles (id) ON DELETE CASCADE
		)`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `
		INSERT IGNORE INTO user_roles (user_id, role_id)
		SELECT mhr.model_id, mhr.role_id
		FROM model_has_roles mhr
		INNER JOIN users u ON u.id = mhr.model_id
		INNER JOIN roles r ON r.id = mhr.role_id
		WHERE mhr.model_type = 'App\\Models\\User'`)
	return err
}

// HasRole reports whether the user has the named role.
func HasRole(ctx context.Context, db *sql.DB, userID int64, role string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, `
		SELECT 1
		FROM user_roles ur
		INNER JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ? AND r.name = ?
		LIMIT 1
	`, userID, role).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GrantRole attaches a role to a user. The role is named in the roles table.
func GrantRole(ctx context.Context, tx *sql.Tx, userID, roleID int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)`, userID, roleID)
	return err
}
