package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"finance-platform/go/internal/money"
	"finance-platform/go/internal/store"

	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"
)

// ChangePassword replaces the caller's password after the current one matches.
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next, confirm string) error {
	if strings.TrimSpace(current) == "" || strings.TrimSpace(next) == "" {
		return invalid(map[string][]string{"password": {"The password field is required."}})
	}
	if next != confirm {
		return invalid(map[string][]string{"password": {"The password field confirmation does not match."}})
	}
	if len(next) < 8 {
		return invalid(map[string][]string{"password": {"The password must be at least 8 characters."}})
	}
	var hash string
	err := s.DB.QueryRowContext(ctx, `SELECT password FROM users WHERE id = ?`, userID).Scan(&hash)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return fail(422, "Неверный текущий пароль")
	}
	nextHash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET password = ?, updated_at = ? WHERE id = ?`, string(nextHash), s.now(), userID)
	return err
}

// UpdateSetting writes one admin settings card.
func (s *Service) UpdateSetting(ctx context.Context, actor int64, key string, body map[string]json.RawMessage) (string, error) {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return "", err
	}
	switch key {
	case "prime-time-bonus":
		starts, ends, rate := rawString(body["starts"]), rawString(body["ends"]), rawString(body["rate"])
		if starts == "" || ends == "" || rate == "" {
			return "", invalid(map[string][]string{"starts": {"The starts field is required."}})
		}
		if err := s.putSetting(ctx, "prime_time_bonus_starts", starts); err != nil {
			return "", err
		}
		if err := s.putSetting(ctx, "prime_time_bonus_ends", ends); err != nil {
			return "", err
		}
		return "Prime time bonus обновлён", s.putSetting(ctx, "prime_time_bonus_rate", rate)
	case "support-link":
		return s.oneSetting(ctx, "support_link", rawString(body["support_link"]), "Ссылка поддержки обновлена")
	case "deposit-link":
		return s.oneSetting(ctx, "deposit_link", rawString(body["deposit_link"]), "Ссылка депозита обновлена")
	case "funds-on-hold":
		return s.oneSetting(ctx, "funds_on_hold_time", rawString(body["hold_time"]), "Время холда обновлено")
	case "max-pending-disputes":
		return s.oneSetting(ctx, "max_pending_disputes", rawString(body["max_pending_disputes"]), "Лимит ожидающих споров обновлён")
	case "max-rejected-disputes":
		payload, _ := json.Marshal(map[string]string{"count": rawString(body["count"]), "period": rawString(body["period"])})
		return "Лимит отклонённых споров обновлён", s.putSetting(ctx, "max_rejected_disputes", string(payload))
	case "platform-wallet":
		return s.oneSetting(ctx, "platform_wallet", rawString(body["platform_wallet"]), "Кошелёк платформы обновлён")
	case "max-consecutive-failed-orders":
		payload, _ := json.Marshal(map[string]string{"count": rawString(body["count"]), "period": rawString(body["period"])})
		return "Лимит неуспешных сделок обновлён", s.putSetting(ctx, "max_consecutive_failed_orders", string(payload))
	default:
		return "", fail(404, "Настройка не найдена")
	}
}

func (s *Service) oneSetting(ctx context.Context, key, value, message string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", invalid(map[string][]string{key: {"The field is required."}})
	}
	return message, s.putSetting(ctx, key, value)
}

func (s *Service) putSetting(ctx context.Context, key, value string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE settings SET `value` = ? WHERE `key` = ?", value, key)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		_, err = s.DB.ExecContext(ctx, "INSERT INTO settings (`key`, `value`) VALUES (?, ?)", key, value)
	}
	return err
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

// AddNote stores a user note.
func (s *Service) AddNote(ctx context.Context, actor, userID int64, content string) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return invalid(map[string][]string{"content": {"The content field is required."}})
	}
	now := s.now()
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO user_notes (user_id, content, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		userID, content, actor, now, now)
	return err
}

// ToggleOnline applies the requested online flags.
func (s *Service) ToggleOnline(ctx context.Context, actor, userID int64, online, payout *int) (bool, bool, error) {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return false, false, err
	}
	var currentOnline, currentPayout, stop int
	var banned sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT is_online, is_payout_online, stop_traffic, banned_at FROM users WHERE id = ?`, userID).
		Scan(&currentOnline, &currentPayout, &stop, &banned)
	if err == sql.ErrNoRows {
		return false, false, fail(404, "Пользователь не найден")
	}
	if err != nil {
		return false, false, err
	}
	if online != nil && *online != currentOnline {
		if *online == 1 && banned.Valid {
			return false, false, fail(400, "Нельзя включить онлайн для заблокированного пользователя")
		}
		if *online == 1 && stop == 1 {
			return false, false, fail(400, "Нельзя включить онлайн при остановленном трафике")
		}
		currentOnline = *online
	}
	if payout != nil && *payout != currentPayout {
		currentPayout = *payout
		active := 0
		if currentPayout == 1 {
			active = 1
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE payout_offers SET active = ?, updated_at = ? WHERE owner_id = ?`, active, s.now(), userID); err != nil {
			return false, false, err
		}
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET is_online = ?, is_payout_online = ?, updated_at = ? WHERE id = ?`, currentOnline, currentPayout, s.now(), userID)
	return currentOnline == 1, currentPayout == 1, err
}

// ToggleTraffic flips stop_traffic for support.
func (s *Service) ToggleTraffic(ctx context.Context, actor, userID int64) error {
	ok, err := s.hasAnyRole(ctx, actor, "Support", "Merchant Support", "Admin")
	if err != nil {
		return err
	}
	if !ok {
		return plain(403, "This action is unauthorized.")
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET stop_traffic = 1 - stop_traffic, updated_at = ? WHERE id = ?`, s.now(), userID)
	return err
}

// Reset2FA clears the stored secret.
func (s *Service) Reset2FA(ctx context.Context, actor, userID int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET google2fa_secret = NULL, updated_at = ? WHERE id = ?`, s.now(), userID)
	return err
}

// BanMerchant sets or clears merchants.banned_at.
func (s *Service) BanMerchant(ctx context.Context, actor, merchantID int64, ban bool) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	var banned any
	if ban {
		banned = s.now()
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE merchants SET banned_at = ?, updated_at = ? WHERE id = ?`, banned, s.now(), merchantID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fail(404, "Проект не найден.")
	}
	return nil
}

// SaveMerchantSettings writes the fields the merchant card edits.
func (s *Service) SaveMerchantSettings(ctx context.Context, actor, merchantID int64, marketName string, wait *int) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `
		UPDATE merchants SET market = COALESCE(NULLIF(?, ''), market), max_order_wait_time = COALESCE(?, max_order_wait_time), updated_at = ?
		WHERE id = ?`, marketName, wait, s.now(), merchantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Проект не найден.")
	}
	return nil
}

// SaveCallback updates one project the caller owns.
func (s *Service) SaveCallback(ctx context.Context, userID, merchantID int64, callback string) error {
	callback = strings.TrimSpace(callback)
	if callback == "" {
		return invalid(map[string][]string{"callback_url": {"The callback url field is required."}})
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE merchants SET callback_url = ?, updated_at = ? WHERE id = ? AND user_id = ?`, callback, s.now(), merchantID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Проект не найден.")
	}
	return nil
}

// SaveGatewaySettings stores the JSON the merchant form sends.
func (s *Service) SaveGatewaySettings(ctx context.Context, userID, merchantID int64, settings json.RawMessage) error {
	if len(settings) == 0 {
		return invalid(map[string][]string{"gateway_settings": {"The gateway settings field is required."}})
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE merchants SET gateway_settings = ?, updated_at = ? WHERE id = ? AND user_id = ?`, string(settings), s.now(), merchantID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Проект не найден.")
	}
	return nil
}

// ToggleDetail flips is_active.
func (s *Service) ToggleDetail(ctx context.Context, actor, detailID int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE payment_details SET is_active = 1 - is_active, updated_at = ? WHERE id = ?`, s.now(), detailID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Реквизит не найден")
	}
	return nil
}

// ArchiveDetail sets or clears archived_at.
func (s *Service) ArchiveDetail(ctx context.Context, actor, detailID int64, archive bool) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	var stamp any
	if archive {
		stamp = s.now()
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE payment_details SET archived_at = ?, updated_at = ? WHERE id = ?`, stamp, s.now(), detailID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Реквизит не найден")
	}
	return nil
}

// SaveOwnDetail updates a requisite the trader owns.
func (s *Service) SaveOwnDetail(ctx context.Context, userID, detailID int64, name, detail, detailType, initials string, active *bool) error {
	var owner int64
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM payment_details WHERE id = ?`, detailID).Scan(&owner)
	if err == sql.ErrNoRows || (err == nil && owner != userID) {
		return plain(403, "This action is unauthorized.")
	}
	if err != nil {
		return err
	}
	activeBit := any(nil)
	if active != nil {
		if *active {
			activeBit = 1
		} else {
			activeBit = 0
		}
	}
	_, err = s.DB.ExecContext(ctx, `
		UPDATE payment_details SET
			name = COALESCE(NULLIF(?, ''), name),
			detail = COALESCE(NULLIF(?, ''), detail),
			detail_type = COALESCE(NULLIF(?, ''), detail_type),
			initials = COALESCE(NULLIF(?, ''), initials),
			is_active = COALESCE(?, is_active),
			updated_at = ?
		WHERE id = ?`, name, detail, detailType, initials, activeBit, s.now(), detailID)
	return err
}

// SaveCategory inserts or updates a category row. table is categories or trader_categories.
func (s *Service) SaveCategory(ctx context.Context, actor int64, table string, id int64, name, description string, active *bool) (int64, error) {
	if table != "categories" && table != "trader_categories" {
		return 0, fail(404, "Категория не найдена")
	}
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return 0, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, invalid(map[string][]string{"name": {"The name field is required."}})
	}
	slug := slug(name)
	now := s.now()
	if id == 0 {
		var res sql.Result
		var err error
		if table == "trader_categories" {
			bit := 1
			if active != nil && !*active {
				bit = 0
			}
			res, err = s.DB.ExecContext(ctx, `INSERT INTO trader_categories (name, slug, description, is_active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, name, slug, description, bit, now, now)
		} else {
			res, err = s.DB.ExecContext(ctx, `INSERT INTO categories (name, slug, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, name, slug, description, now, now)
		}
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	query := `UPDATE ` + table + ` SET name = ?, slug = ?, description = ?, updated_at = ? WHERE id = ?`
	res, err := s.DB.ExecContext(ctx, query, name, slug, description, now, id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, fail(404, "Категория не найдена")
	}
	return id, nil
}

// DeleteCategory removes a category row.
func (s *Service) DeleteCategory(ctx context.Context, actor int64, table string, id int64) error {
	if table != "categories" && table != "trader_categories" {
		return fail(404, "Категория не найдена")
	}
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM `+table+` WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Категория не найдена")
	}
	return nil
}

// AssignTrader sets users.trader_category_id.
func (s *Service) AssignTrader(ctx context.Context, actor, categoryID, traderID int64, assign bool) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	var value any
	if assign {
		value = categoryID
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET trader_category_id = ?, updated_at = ? WHERE id = ?`, value, s.now(), traderID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Трейдер не найден")
	}
	return nil
}

// ClearCategoryTraders detaches every trader from a category.
func (s *Service) ClearCategoryTraders(ctx context.Context, actor, categoryID int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET trader_category_id = NULL, updated_at = ? WHERE trader_category_id = ?`, s.now(), categoryID)
	return err
}

// SavePriority inserts a merchant category priority.
func (s *Service) SavePriority(ctx context.Context, actor, merchantID, categoryID int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	var merchant int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM merchants WHERE id = ?`, merchantID).Scan(&merchant)
	if err == sql.ErrNoRows {
		return fail(404, "Проект не найден")
	}
	if err != nil {
		return err
	}
	var category int
	err = s.DB.QueryRowContext(ctx, `SELECT 1 FROM trader_categories WHERE id = ?`, categoryID).Scan(&category)
	if err == sql.ErrNoRows {
		return invalid(map[string][]string{"category_id": {"The selected category id is invalid."}})
	}
	if err != nil {
		return err
	}
	var next int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(priority), 0) + 1 FROM merchant_trader_category_priorities WHERE merchant_id = ?`, merchantID).Scan(&next); err != nil {
		return err
	}
	now := s.now()
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO merchant_trader_category_priorities (merchant_id, trader_category_id, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, merchantID, categoryID, next, now, now)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		return invalid(map[string][]string{"category_id": {"Категория уже добавлена."}})
	}
	return err
}

// DeletePriority removes one merchant category priority.
func (s *Service) DeletePriority(ctx context.Context, actor, merchantID, categoryID int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM merchant_trader_category_priorities WHERE merchant_id = ? AND trader_category_id = ?`, merchantID, categoryID)
	return err
}

// SavePromo creates or updates a promo code.
func (s *Service) SavePromo(ctx context.Context, actor, id, leaderID int64, code string, maxUses int, active bool) (int64, error) {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return 0, err
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return 0, invalid(map[string][]string{"code": {"The code field is required."}})
	}
	bit := 0
	if active {
		bit = 1
	}
	now := s.now()
	if id == 0 {
		res, err := s.DB.ExecContext(ctx, `
			INSERT INTO promo_codes (code, max_uses, used_count, is_active, team_leader_id, created_at, updated_at)
			VALUES (?, ?, 0, ?, ?, ?, ?)`, code, maxUses, bit, leaderID, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	res, err := s.DB.ExecContext(ctx, `
		UPDATE promo_codes SET code = ?, max_uses = ?, is_active = ?, team_leader_id = ?, updated_at = ? WHERE id = ?`,
		code, maxUses, bit, leaderID, now, id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, fail(404, "Промокод не найден")
	}
	return id, nil
}

// DeletePromo removes a promo code.
func (s *Service) DeletePromo(ctx context.Context, actor, id int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM promo_codes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Промокод не найден")
	}
	return nil
}

// SaveWord inserts a stop or block word.
func (s *Service) SaveWord(ctx context.Context, actor int64, table, word string) (int64, error) {
	if table != "sms_stop_words" && table != "sms_block_words" && table != "sender_stop_lists" {
		return 0, fail(404, "Список не найден")
	}
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return 0, err
	}
	word = strings.TrimSpace(word)
	if word == "" {
		return 0, invalid(map[string][]string{"word": {"The word field is required."}})
	}
	column := "word"
	if table == "sender_stop_lists" {
		column = "sender"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO `+table+` (`+column+`) VALUES (?)`, word)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteWord removes a stop or block word.
func (s *Service) DeleteWord(ctx context.Context, actor int64, table string, id int64) error {
	if table != "sms_stop_words" && table != "sms_block_words" && table != "sender_stop_lists" {
		return fail(404, "Список не найден")
	}
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM `+table+` WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Запись не найдена")
	}
	return nil
}

// SenderFromSMS copies the sender of a log into the stop list.
func (s *Service) SenderFromSMS(ctx context.Context, actor, logID int64) (int64, error) {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return 0, err
	}
	var sender sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT sender FROM sms_logs WHERE id = ?`, logID).Scan(&sender)
	if err == sql.ErrNoRows || !sender.Valid || strings.TrimSpace(sender.String) == "" {
		return 0, fail(404, "SMS не найдено")
	}
	if err != nil {
		return 0, err
	}
	return s.SaveWord(ctx, actor, "sender_stop_lists", sender.String)
}

// SaveNotification stores an admin broadcast row.
func (s *Service) SaveNotification(ctx context.Context, actor int64, message string) (int64, error) {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return 0, err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return 0, invalid(map[string][]string{"message": {"The message field is required."}})
	}
	now := s.now()
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO notifications (message, recipients_count, delivered_count, created_at, updated_at)
		VALUES (?, 0, 0, ?, ?)`, message, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ToggleProvider flips providers.is_active.
func (s *Service) ToggleProvider(ctx context.Context, actor, id int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE providers SET is_active = 1 - is_active, updated_at = ? WHERE id = ?`, s.now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Провайдер не найден")
	}
	return nil
}

// ToggleTerminal flips provider_terminals.is_active.
func (s *Service) ToggleTerminal(ctx context.Context, actor, id int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE provider_terminals SET is_active = 1 - is_active, updated_at = ? WHERE id = ?`, s.now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Терминал не найден")
	}
	return nil
}

func (s *Service) hasAnyRole(ctx context.Context, userID int64, roles ...string) (bool, error) {
	for _, role := range roles {
		ok, err := store.HasRole(ctx, s.DB, userID, role)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func slug(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}

// GatewayForm is the payment-method card the admin cabinet saves.
type GatewayForm struct {
	Name, Code, NSPK, Currency    string
	MinLimit, MaxLimit            string
	TraderOrders, TraderPayouts   string
	ServiceOrders, ServicePayouts string
	Active, Intrabank             bool
	Transgran                     *bool
	ReserveOrders, ReservePayouts int
	DetailTypes, Senders          []string
}

// SaveGateway inserts or updates a payment method. A missing logo file is left empty:
// the cabinet form does not attach one.
func (s *Service) SaveGateway(ctx context.Context, actor, id int64, form GatewayForm) (int64, error) {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return 0, err
	}
	fields := map[string][]string{}
	form.Name = strings.TrimSpace(form.Name)
	form.Code = strings.TrimSpace(form.Code)
	form.NSPK = strings.TrimSpace(form.NSPK)
	form.Currency = strings.ToLower(strings.TrimSpace(form.Currency))
	if len(form.Name) < 3 || len(form.Name) > 30 {
		fields["name"] = []string{"The name must be between 3 and 30 characters."}
	}
	if len(form.Code) < 3 || len(form.Code) > 30 {
		fields["code"] = []string{"The code must be between 3 and 30 characters."}
	}
	if len(form.NSPK) < 3 || len(form.NSPK) > 50 {
		fields["nspk_schema"] = []string{"The nspk schema must be between 3 and 50 characters."}
	}
	if !knownCurrency(form.Currency) {
		fields["currency"] = []string{"The selected currency is invalid."}
	}
	types := make([]string, 0, len(form.DetailTypes))
	for _, item := range form.DetailTypes {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item != "card" && item != "phone" && item != "account_number" && item != "sim" {
			fields["detail_types"] = []string{"The selected detail types is invalid."}
			break
		}
		if form.Intrabank && item == "phone" {
			continue
		}
		types = append(types, item)
	}
	if len(types) == 0 && fields["detail_types"] == nil {
		fields["detail_types"] = []string{"The detail types field is required."}
	}
	if !whole(form.MinLimit) {
		fields["min_limit"] = []string{"The min limit must be at least 1."}
	}
	if !whole(form.MaxLimit) {
		fields["max_limit"] = []string{"The max limit must be at least 1."}
	}
	for key, value := range map[string]string{
		"trader_commission_rate_for_orders":         form.TraderOrders,
		"trader_commission_rate_for_payouts":        form.TraderPayouts,
		"total_service_commission_rate_for_orders":  form.ServiceOrders,
		"total_service_commission_rate_for_payouts": form.ServicePayouts,
	} {
		if !rate(value) {
			fields[key] = []string{"The " + strings.ReplaceAll(key, "_", " ") + " must be at least 0."}
		}
	}
	if form.ReserveOrders < 1 {
		fields["reservation_time_for_orders"] = []string{"The reservation time for orders must be at least 1."}
	}
	if form.ReservePayouts < 1 {
		fields["reservation_time_for_payouts"] = []string{"The reservation time for payouts must be at least 1."}
	}
	if len(fields) > 0 {
		return 0, invalid(fields)
	}
	var taken int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_gateways WHERE code = ? AND id <> ?`, form.Code, id).Scan(&taken); err != nil {
		return 0, err
	}
	if taken > 0 {
		return 0, invalid(map[string][]string{"code": {"The code has already been taken."}})
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_gateways WHERE nspk_schema = ? AND id <> ?`, form.NSPK, id).Scan(&taken); err != nil {
		return 0, err
	}
	if taken > 0 {
		return 0, invalid(map[string][]string{"nspk_schema": {"The nspk schema has already been taken."}})
	}
	detailJSON, _ := json.Marshal(types)
	senderJSON, _ := json.Marshal(form.Senders)
	active, intra := 0, 0
	if form.Active {
		active = 1
	}
	if form.Intrabank {
		intra = 1
	}
	if id == 0 {
		res, err := s.DB.ExecContext(ctx, `
			INSERT INTO payment_gateways (
				name, code, nspk_schema, currency, min_limit, max_limit, sms_senders,
				trader_commission_rate_for_orders, trader_commission_rate_for_payouts,
				total_service_commission_rate_for_orders, total_service_commission_rate_for_payouts,
				is_active, is_intrabank, detail_types, reservation_time_for_orders, reservation_time_for_payouts
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			form.Name, form.Code, form.NSPK, form.Currency, form.MinLimit, form.MaxLimit, string(senderJSON),
			form.TraderOrders, form.TraderPayouts, form.ServiceOrders, form.ServicePayouts,
			active, intra, string(detailJSON), form.ReserveOrders, form.ReservePayouts)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	transgranSQL := ""
	args := []any{
		form.Name, form.Code, form.NSPK, form.Currency, form.MinLimit, form.MaxLimit, string(senderJSON),
		form.TraderOrders, form.TraderPayouts, form.ServiceOrders, form.ServicePayouts,
		active, intra, string(detailJSON), form.ReserveOrders, form.ReservePayouts,
	}
	if form.Transgran != nil {
		bit := 0
		if *form.Transgran {
			bit = 1
		}
		transgranSQL = ", is_transgran = ?"
		args = append(args, bit)
	}
	args = append(args, id)
	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_gateways WHERE id = ?`, id).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, fail(404, "Платёжный метод не найден")
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE payment_gateways SET
			name = ?, code = ?, nspk_schema = ?, currency = ?, min_limit = ?, max_limit = ?, sms_senders = ?,
			trader_commission_rate_for_orders = ?, trader_commission_rate_for_payouts = ?,
			total_service_commission_rate_for_orders = ?, total_service_commission_rate_for_payouts = ?,
			is_active = ?, is_intrabank = ?, detail_types = ?, reservation_time_for_orders = ?, reservation_time_for_payouts = ?`+transgranSQL+`
		WHERE id = ?`, args...)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// DeleteGateway removes a method that has no orders and no requisites.
func (s *Service) DeleteGateway(ctx context.Context, actor, id int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	var orders, details int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE payment_gateway_id = ?`, id).Scan(&orders); err != nil {
		return err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_detail_payment_gateway WHERE payment_gateway_id = ?`, id).Scan(&details); err != nil {
		return err
	}
	if orders > 0 || details > 0 {
		return fail(422, "Нельзя удалить метод: есть связанные сделки или реквизиты")
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM payment_gateways WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "Платёжный метод не найден")
	}
	return nil
}

func knownCurrency(code string) bool {
	for _, item := range money.All() {
		if item.Code() == code {
			return true
		}
	}
	return false
}

func whole(value string) bool {
	number, err := decimal.NewFromString(strings.TrimSpace(value))
	return err == nil && number.Equal(number.Truncate(0)) && number.GreaterThanOrEqual(decimal.NewFromInt(1))
}

func rate(value string) bool {
	number, err := decimal.NewFromString(strings.TrimSpace(value))
	return err == nil && !number.IsNegative()
}
