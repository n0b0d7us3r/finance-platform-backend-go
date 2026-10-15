package impersonate

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"
)

const (
	HandshakeTTL = 60 * time.Second
	SessionTTL   = 30 * time.Minute
	HardCap      = 2 * time.Hour
)

// Service stores impersonation in impersonation_sessions.
// A session token is resolved before the 24-hour access-token cache and is never written there.
type Service struct {
	DB          *sql.DB
	Key         []byte
	Now         func() time.Time
	Handshake   time.Duration
	Session     time.Duration
	Cap         time.Duration
	MerchantURL string
	TraderURL   string
	SupportURL  string
	LeaderURL   string
}

// Decision is the result of looking up an Access-Token as a viewing session.
type Decision struct {
	Found    bool
	Allowed  bool
	ActorID  int64
	TargetID int64
}

type activeKey struct{}

// Active is the viewing session attached to a request.
type Active struct {
	ActorID  int64
	TargetID int64
}

// WithActive marks the request as a viewing session.
func WithActive(ctx context.Context, active Active) context.Context {
	return context.WithValue(ctx, activeKey{}, active)
}

// FromContext returns the viewing session when the Access-Token is one.
func FromContext(ctx context.Context) (Active, bool) {
	active, ok := ctx.Value(activeKey{}).(Active)
	return active, ok
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) handshake() time.Duration {
	if s.Handshake > 0 {
		return s.Handshake
	}
	return HandshakeTTL
}

func (s *Service) session() time.Duration {
	if s.Session > 0 {
		return s.Session
	}
	return SessionTTL
}

func (s *Service) cap() time.Duration {
	limit := s.Cap
	if limit <= 0 {
		limit = HardCap
	}
	if limit < s.session() {
		return s.session()
	}
	return limit
}

// Start opens a one-minute handshake for a cabinet user.
func (s *Service) Start(ctx context.Context, actor, target int64, ip, ua string) (handshake, cabinet string, expires int, err error) {
	if actor == target {
		return "", "", 0, errStatus(403, "Нельзя открыть свой собственный кабинет")
	}
	admin, err := s.hasRole(ctx, actor, "Admin")
	if err != nil {
		return "", "", 0, err
	}
	if !admin {
		return "", "", 0, errStatus(403, "Только Admin может открыть кабинет пользователя")
	}
	ok, role, err := s.eligible(ctx, target)
	if err != nil {
		return "", "", 0, err
	}
	if !ok {
		return "", "", 0, errStatus(403, "Можно открыть только кабинет Merchant, Trader, Support, Merchant Support или Team Leader без бана")
	}
	code, err := plain()
	if err != nil {
		return "", "", 0, err
	}
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", "", 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE impersonation_sessions SET revoked_at = ?, updated_at = ?
		WHERE actor_id = ? AND target_id = ? AND revoked_at IS NULL`, now, now, actor, target); err != nil {
		return "", "", 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO impersonation_sessions (
			handshake_hash, actor_id, target_id, expires_at, ip, ua, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.hash(code), actor, target, now.Add(s.handshake()), ip, ua, now, now); err != nil {
		return "", "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", "", 0, err
	}
	return code, s.cabinet(role), int(s.handshake() / time.Second), nil
}

// Consume trades a handshake for a session token.
func (s *Service) Consume(ctx context.Context, code string, roles []string, ip, ua string) (map[string]any, error) {
	var (
		id, actorID, targetID int64
		expires, created      time.Time
		actorEmail            sql.NullString
	)
	err := s.DB.QueryRowContext(ctx, `
		SELECT s.id, s.actor_id, s.target_id, s.expires_at, s.created_at, actor.email
		FROM impersonation_sessions s
		LEFT JOIN users actor ON actor.id = s.actor_id
		WHERE s.handshake_hash = ? AND s.consumed_at IS NULL AND s.revoked_at IS NULL`, s.hash(code)).
		Scan(&id, &actorID, &targetID, &expires, &created, &actorEmail)
	if err == sql.ErrNoRows {
		return nil, errStatus(401, "Недействительный или уже использованный код")
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if !expires.After(now) {
		_ = s.revoke(ctx, id, now)
		return nil, errStatus(401, "Код истек")
	}
	ok, role, err := s.eligible(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if !ok || !contains(roles, role) {
		_ = s.revoke(ctx, id, now)
		return nil, errStatus(403, "Пользователь недоступен для просмотра")
	}
	token, err := plain()
	if err != nil {
		return nil, err
	}
	until := until(now, created, s.session(), s.cap())
	var name, email string
	if err := s.DB.QueryRowContext(ctx, `SELECT name, email FROM users WHERE id = ?`, targetID).Scan(&name, &email); err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `
		UPDATE impersonation_sessions
		SET session_token_hash = ?, consumed_at = ?, expires_at = ?, ip = COALESCE(NULLIF(?, ''), ip), ua = COALESCE(NULLIF(?, ''), ua), updated_at = ?
		WHERE id = ?`, s.hash(token), now, until, ip, ua, now, id); err != nil {
		return nil, err
	}
	return map[string]any{
		"token":  token,
		"actor":  map[string]any{"id": actorID, "email": actorEmail.String},
		"target": map[string]any{"id": targetID, "name": name, "email": email, "role": role},
		"until":  until.Format(time.RFC3339),
	}, nil
}

// Leave revokes the session carried by the Access-Token.
func (s *Service) Leave(ctx context.Context, token string) error {
	var id int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT id FROM impersonation_sessions
		WHERE session_token_hash = ? AND consumed_at IS NOT NULL AND revoked_at IS NULL`, s.hash(token)).Scan(&id)
	if err == sql.ErrNoRows {
		return errStatus(403, "Нет активной сессии просмотра")
	}
	if err != nil {
		return err
	}
	return s.revoke(ctx, id, s.now())
}

// Lookup resolves a session token. Found sessions are not cached.
// An expired row is revoked here, so the caller does not edit revoked_at.
func (s *Service) Lookup(ctx context.Context, token, path string) (Decision, error) {
	if strings.TrimSpace(token) == "" || s.DB == nil {
		return Decision{}, nil
	}
	var (
		id, actorID, targetID int64
		expires, created      time.Time
		banned                sql.NullTime
	)
	err := s.DB.QueryRowContext(ctx, `
		SELECT s.id, s.actor_id, s.target_id, s.expires_at, s.created_at, u.banned_at
		FROM impersonation_sessions s
		INNER JOIN users u ON u.id = s.target_id
		WHERE s.session_token_hash = ? AND s.consumed_at IS NOT NULL AND s.revoked_at IS NULL`, s.hash(token)).
		Scan(&id, &actorID, &targetID, &expires, &created, &banned)
	if err == sql.ErrNoRows {
		return Decision{}, nil
	}
	if err != nil {
		return Decision{}, err
	}
	now := s.now()
	if !expires.After(now) || !created.Add(s.cap()).After(now) || banned.Valid {
		_ = s.revoke(ctx, id, now)
		return Decision{Found: true}, nil
	}
	role, err := s.primaryRole(ctx, targetID)
	if err != nil {
		return Decision{}, err
	}
	if !allows(path, role) {
		return Decision{Found: true, ActorID: actorID, TargetID: targetID}, nil
	}
	return Decision{Found: true, Allowed: true, ActorID: actorID, TargetID: targetID}, nil
}

func (s *Service) revoke(ctx context.Context, id int64, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE impersonation_sessions SET revoked_at = ?, updated_at = ? WHERE id = ? AND revoked_at IS NULL`, now, now, id)
	return err
}

func (s *Service) hash(value string) string {
	mac := hmac.New(sha256.New, s.Key)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) eligible(ctx context.Context, userID int64) (bool, string, error) {
	var banned sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT banned_at FROM users WHERE id = ?`, userID).Scan(&banned)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if banned.Valid {
		return false, "", nil
	}
	role, err := s.primaryRole(ctx, userID)
	if err != nil {
		return false, "", err
	}
	switch role {
	case "Merchant", "Trader", "Support", "Merchant Support", "Team Leader":
		return true, role, nil
	default:
		return false, role, nil
	}
}

func (s *Service) primaryRole(ctx context.Context, userID int64) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, `
		SELECT r.name FROM user_roles ur
		INNER JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ?
		ORDER BY r.id LIMIT 1`, userID).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return name, err
}

func (s *Service) hasRole(ctx context.Context, userID int64, role string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_roles ur
		INNER JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ? AND r.name = ?`, userID, role).Scan(&n)
	return n > 0, err
}

func (s *Service) cabinet(role string) string {
	switch role {
	case "Trader":
		return strings.TrimRight(s.TraderURL, "/")
	case "Support", "Merchant Support":
		return strings.TrimRight(s.SupportURL, "/")
	case "Team Leader":
		return strings.TrimRight(s.LeaderURL, "/")
	default:
		return strings.TrimRight(s.MerchantURL, "/")
	}
}

func until(now, created time.Time, session, cap time.Duration) time.Time {
	soft := now.Add(session)
	hard := created.Add(cap)
	if soft.Before(hard) {
		return soft
	}
	return hard
}

func allows(path, role string) bool {
	switch {
	case strings.Contains(path, "frontend/merchant"):
		return role == "Merchant"
	case strings.Contains(path, "frontend/trader"):
		return role == "Trader"
	case strings.Contains(path, "frontend/support"):
		return role == "Support" || role == "Merchant Support"
	case strings.Contains(path, "frontend/leader"):
		return role == "Team Leader"
	default:
		return false
	}
}

func contains(roles []string, role string) bool {
	for _, item := range roles {
		if item == role {
			return true
		}
	}
	return false
}

func plain() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// StatusError is a client-visible impersonation failure.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return e.Message }

func errStatus(status int, message string) error {
	return &StatusError{Status: status, Message: message}
}
