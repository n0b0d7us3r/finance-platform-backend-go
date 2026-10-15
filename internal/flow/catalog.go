package flow

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
)

// Device is a trader phone token.
type Device struct {
	ID    int64
	Token string
	Name  string
}

// CreateDevice issues a device token for the trader.
func (s *Service) CreateDevice(ctx context.Context, traderID int64, name string) (Device, error) {
	if err := s.requireRole(ctx, traderID, "Trader"); err != nil {
		return Device{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Device{}, invalid(map[string][]string{"name": {"The name field is required."}})
	}
	token, err := randomToken()
	if err != nil {
		return Device{}, err
	}
	now := s.now()
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO user_devices (user_id, name, token, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, traderID, name, token, now, now)
	if err != nil {
		return Device{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Device{}, err
	}
	return Device{ID: id, Token: token, Name: name}, nil
}

// ConnectDevice marks the phone connected. Access-Token is the device token.
func (s *Service) ConnectDevice(ctx context.Context, token, androidID, model, version, manufacturer, brand string) (Device, error) {
	if androidID == "" || model == "" {
		return Device{}, invalid(map[string][]string{
			"android_id":   {"The android id field is required."},
			"device_model": {"The device model field is required."},
		})
	}
	var device Device
	var current sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, token, android_id FROM user_devices WHERE token = ?`, token).
		Scan(&device.ID, &device.Name, &device.Token, &current)
	if err == sql.ErrNoRows {
		return Device{}, plain(400, "Invalid Access Token.")
	}
	if err != nil {
		return Device{}, err
	}
	if current.Valid && current.String != "" && current.String != androidID {
		return Device{}, fail(400, "Устройство уже подключено с другим android_id.")
	}
	now := s.now()
	_, err = s.DB.ExecContext(ctx, `
		UPDATE user_devices
		SET android_id = ?, device_model = ?, android_version = ?, manufacturer = ?, brand = ?,
		    connected_at = COALESCE(connected_at, ?), updated_at = ?
		WHERE id = ?`, androidID, model, version, manufacturer, brand, now, now, device.ID)
	return device, err
}

// CreateMerchant inserts a project owned by the merchant user.
func (s *Service) CreateMerchant(ctx context.Context, userID int64, name, description, projectLink string) (int64, string, error) {
	if err := s.requireRole(ctx, userID, "Merchant"); err != nil {
		return 0, "", err
	}
	if strings.TrimSpace(name) == "" {
		return 0, "", invalid(map[string][]string{"name": {"The name field is required."}})
	}
	merchantUUID, err := newUUID()
	if err != nil {
		return 0, "", err
	}
	host := projectLink
	if parsed, err := url.Parse(projectLink); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	now := s.now()
	// The demo dump's orders.merchant_id foreign key points at users.id, while the
	// application stores merchants.id. Using the owner's user id for both keeps
	// new projects insertable and readable.
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO merchants (
			id, uuid, user_id, active, name, description, domain, settings, gateway_settings, market, created_at, updated_at
		) VALUES (?, ?, ?, 1, ?, ?, ?, '[]', '[]', 'bybit', ?, ?)`,
		userID, merchantUUID, userID, name, description, host, now, now)
	if err != nil {
		return 0, "", err
	}
	return userID, merchantUUID, nil
}

// CreatePaymentDetail stores a trader requisite and binds one gateway.
func (s *Service) CreatePaymentDetail(ctx context.Context, traderID int64, name, detail, detailType, initials, currency, gatewayCode string, deviceID int64) (int64, error) {
	if err := s.requireRole(ctx, traderID, "Trader"); err != nil {
		return 0, err
	}
	if name == "" || detail == "" || detailType == "" || initials == "" || currency == "" || gatewayCode == "" {
		return 0, invalid(map[string][]string{
			"currency":    {"The currency field is required."},
			"detail":      {"The detail field is required."},
			"detail_type": {"The detail type field is required."},
			"gateway":     {"The gateway field is required."},
			"initials":    {"The initials field is required."},
			"name":        {"The name field is required."},
		})
	}
	gateway, err := s.gatewayByCode(ctx, gatewayCode)
	if err != nil {
		return 0, err
	}
	if !contains(gateway.DetailTypes, detailType) {
		return 0, fail(400, "Платёжный метод не поддерживает этот тип реквизита.")
	}
	if !strings.EqualFold(gateway.Currency, currency) {
		return 0, fail(400, "Валюта реквизита не совпадает с методом.")
	}
	now := s.now()
	var id int64
	err = s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO payment_details (
				name, detail, detail_type, initials, is_active, is_external,
				daily_limit, current_daily_limit, max_pending_orders_quantity,
				min_order_amount, max_order_amount, order_interval_minutes,
				currency, user_id, user_device_id, created_at, updated_at
			) VALUES (?, ?, ?, ?, 1, 0, '10000000000', '0', 5, '0', '0', 0, ?, ?, ?, ?, ?)`,
			name, detail, detailType, initials, strings.ToLower(currency), traderID, deviceID, now, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO payment_detail_payment_gateway (payment_detail_id, payment_gateway_id, created_at, updated_at)
			VALUES (?, ?, ?, ?)`, id, gateway.ID, now, now)
		return err
	})
	return id, err
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
