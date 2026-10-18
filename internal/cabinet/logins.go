package cabinet

import (
	"context"
	"database/sql"
	"strconv"

	"finance-platform/go/internal/auditlog"
)

// UserLogins is the admin list of cabinet sign-ins.
func (r *Reader) UserLogins(ctx context.Context, page Page) (map[string]any, error) {
	total, err := r.count(ctx, `SELECT COUNT(*) FROM user_logins`)
	if err != nil {
		return nil, err
	}
	rows, err := r.DB.QueryContext(ctx, `
		SELECT l.id, l.ip, COALESCE(l.user_agent, ''), l.created_at,
		       u.id, COALESCE(u.name, ''), COALESCE(u.email, ''), COALESCE(MAX(role.name), '')
		FROM user_logins l
		INNER JOIN users u ON u.id = l.user_id
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		LEFT JOIN roles role ON role.id = ur.role_id
		GROUP BY l.id, l.ip, l.user_agent, l.created_at, u.id, u.name, u.email
		ORDER BY l.id DESC
		LIMIT ? OFFSET ?`, page.Size, page.offset())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, userID int64
		var ip, agent, name, email, role string
		var created sql.NullTime
		if err := rows.Scan(&id, &ip, &agent, &created, &userID, &name, &email, &role); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "ip": ip, "user_agent": agent, "created_at": r.when(created),
			"user": map[string]any{"id": userID, "name": name, "email": email, "role": role},
		})
	}
	return Block(page, total, items), rows.Err()
}

// SecurityActions is the admin history of bulk traffic controls.
func (r *Reader) SecurityActions(ctx context.Context, limit int) ([]auditlog.Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.DB.QueryContext(ctx, `
		SELECT s.id, s.user_id, COALESCE(u.name, ''), COALESCE(u.email, ''), s.action, s.label, s.created_at
		FROM security_actions s
		LEFT JOIN users u ON u.id = s.user_id
		ORDER BY s.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []auditlog.Entry{}
	for rows.Next() {
		var id, userID int64
		var name, email, action, label string
		var created sql.NullTime
		if err := rows.Scan(&id, &userID, &name, &email, &action, &label, &created); err != nil {
			return nil, err
		}
		when, _ := r.when(created).(string)
		items = append(items, auditlog.Entry{
			ID:        strconv.FormatInt(id, 10),
			UserID:    userID,
			UserName:  name,
			UserEmail: email,
			Action:    action,
			Label:     label,
			CreatedAt: when,
		})
	}
	return items, rows.Err()
}
