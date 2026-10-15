package flow

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// SecurityResult is how many users a bulk security action changed, plus tokens that must leave the cache.
type SecurityResult struct {
	Affected int
	Tokens   []string
}

// RecordLogin stores one successful cabinet sign-in.
func (s *Service) RecordLogin(ctx context.Context, userID int64, ip, userAgent string) error {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "—"
	}
	userAgent = strings.TrimSpace(userAgent)
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO user_logins (user_id, ip, user_agent, created_at) VALUES (?, ?, ?, ?)`,
		userID, ip, userAgent, s.now())
	return err
}

// SecurityAction runs one admin bulk control for traders or merchants.
func (s *Service) SecurityAction(ctx context.Context, actor int64, action string) (SecurityResult, error) {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return SecurityResult{}, err
	}
	now := s.now()
	switch action {
	case "stop_traders":
		return s.stopRoleTraffic(ctx, "Trader", now)
	case "stop_merchants":
		return s.stopRoleTraffic(ctx, "Merchant", now)
	case "stop_users":
		return s.stopNonAdminTraffic(ctx, now)
	case "ban_traders":
		return s.banRole(ctx, "Trader", now, false)
	case "ban_merchants":
		return s.banRole(ctx, "Merchant", now, true)
	case "ban_users":
		return s.banNonAdmin(ctx, now)
	case "reset_trader_tokens":
		return s.resetRoleTokens(ctx, "Trader", now)
	case "reset_merchant_tokens":
		return s.resetRoleTokens(ctx, "Merchant", now)
	case "reset_user_tokens":
		return s.resetNonAdminTokens(ctx, now)
	default:
		return SecurityResult{}, fail(422, "Неизвестное действие.")
	}
}

func (s *Service) stopRoleTraffic(ctx context.Context, role string, now time.Time) (SecurityResult, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE users u
		INNER JOIN user_roles ur ON ur.user_id = u.id
		INNER JOIN roles r ON r.id = ur.role_id AND r.name = ?
		SET u.stop_traffic = 1, u.traffic_enabled_at = NULL, u.updated_at = ?`, role, now)
	if err != nil {
		return SecurityResult{}, err
	}
	n, _ := res.RowsAffected()
	return SecurityResult{Affected: int(n)}, nil
}

func (s *Service) banRole(ctx context.Context, role string, now time.Time, projects bool) (SecurityResult, error) {
	var affected int
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE users u
			INNER JOIN user_roles ur ON ur.user_id = u.id
			INNER JOIN roles r ON r.id = ur.role_id AND r.name = ?
			SET u.banned_at = COALESCE(u.banned_at, ?), u.is_online = 0, u.is_payout_online = 0, u.updated_at = ?`,
			role, now, now)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		affected = int(n)
		if !projects {
			return nil
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE merchants m
			INNER JOIN users u ON u.id = m.user_id
			INNER JOIN user_roles ur ON ur.user_id = u.id
			INNER JOIN roles r ON r.id = ur.role_id AND r.name = ?
			SET m.banned_at = COALESCE(m.banned_at, ?), m.updated_at = ?`, role, now, now)
		return err
	})
	if err != nil {
		return SecurityResult{}, err
	}
	return SecurityResult{Affected: affected}, nil
}

func (s *Service) resetRoleTokens(ctx context.Context, role string, now time.Time) (SecurityResult, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.api_access_token, '')
		FROM users u
		INNER JOIN user_roles ur ON ur.user_id = u.id
		INNER JOIN roles r ON r.id = ur.role_id AND r.name = ?`, role)
	if err != nil {
		return SecurityResult{}, err
	}
	defer rows.Close()
	type account struct {
		id    int64
		token string
	}
	var accounts []account
	for rows.Next() {
		var item account
		if err := rows.Scan(&item.id, &item.token); err != nil {
			return SecurityResult{}, err
		}
		accounts = append(accounts, item)
	}
	if err := rows.Err(); err != nil {
		return SecurityResult{}, err
	}
	old := make([]string, 0, len(accounts))
	err = s.tx(ctx, func(tx *sql.Tx) error {
		for _, item := range accounts {
			token, err := randomToken()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET api_access_token = ?, updated_at = ? WHERE id = ?`, token, now, item.id); err != nil {
				return err
			}
			if item.token != "" {
				old = append(old, item.token)
			}
		}
		return nil
	})
	if err != nil {
		return SecurityResult{}, err
	}
	return SecurityResult{Affected: len(accounts), Tokens: old}, nil
}

func (s *Service) stopNonAdminTraffic(ctx context.Context, now time.Time) (SecurityResult, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE users u
		INNER JOIN user_roles ur ON ur.user_id = u.id
		INNER JOIN roles r ON r.id = ur.role_id AND r.name <> 'Admin'
		SET u.stop_traffic = 1, u.traffic_enabled_at = NULL, u.updated_at = ?`, now)
	if err != nil {
		return SecurityResult{}, err
	}
	n, _ := res.RowsAffected()
	return SecurityResult{Affected: int(n)}, nil
}

func (s *Service) banNonAdmin(ctx context.Context, now time.Time) (SecurityResult, error) {
	var affected int
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE users u
			INNER JOIN user_roles ur ON ur.user_id = u.id
			INNER JOIN roles r ON r.id = ur.role_id AND r.name <> 'Admin'
			SET u.banned_at = COALESCE(u.banned_at, ?), u.is_online = 0, u.is_payout_online = 0, u.updated_at = ?`,
			now, now)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		affected = int(n)
		_, err = tx.ExecContext(ctx, `
			UPDATE merchants m
			INNER JOIN users u ON u.id = m.user_id
			INNER JOIN user_roles ur ON ur.user_id = u.id
			INNER JOIN roles r ON r.id = ur.role_id AND r.name <> 'Admin'
			SET m.banned_at = COALESCE(m.banned_at, ?), m.updated_at = ?`, now, now)
		return err
	})
	if err != nil {
		return SecurityResult{}, err
	}
	return SecurityResult{Affected: affected}, nil
}

func (s *Service) resetNonAdminTokens(ctx context.Context, now time.Time) (SecurityResult, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.api_access_token, '')
		FROM users u
		INNER JOIN user_roles ur ON ur.user_id = u.id
		INNER JOIN roles r ON r.id = ur.role_id AND r.name <> 'Admin'`)
	if err != nil {
		return SecurityResult{}, err
	}
	defer rows.Close()
	type account struct {
		id    int64
		token string
	}
	var accounts []account
	for rows.Next() {
		var item account
		if err := rows.Scan(&item.id, &item.token); err != nil {
			return SecurityResult{}, err
		}
		accounts = append(accounts, item)
	}
	if err := rows.Err(); err != nil {
		return SecurityResult{}, err
	}
	old := make([]string, 0, len(accounts))
	err = s.tx(ctx, func(tx *sql.Tx) error {
		for _, item := range accounts {
			token, err := randomToken()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET api_access_token = ?, updated_at = ? WHERE id = ?`, token, now, item.id); err != nil {
				return err
			}
			if item.token != "" {
				old = append(old, item.token)
			}
		}
		return nil
	})
	if err != nil {
		return SecurityResult{}, err
	}
	return SecurityResult{Affected: len(accounts), Tokens: old}, nil
}
