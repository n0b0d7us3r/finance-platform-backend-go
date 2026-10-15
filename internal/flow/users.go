package flow

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"finance-platform/go/internal/market"
	"finance-platform/go/internal/money"
	"finance-platform/go/internal/store"

	"golang.org/x/crypto/bcrypt"
)

// Session is a cabinet login.
type Session struct {
	Token  string
	UserID int64
	Name   string
	Email  string
	Role   string
}

// NewUser is an admin-created account.
type NewUser struct {
	ID    int64
	Email string
}

// Login checks the password and the cabinet role, then returns the API token.
func (s *Service) Login(ctx context.Context, email, password, role string) (Session, error) {
	var session Session
	var hash string
	var banned sql.NullTime
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, name, email, password, api_access_token, banned_at
		FROM users WHERE email = ?`, email).
		Scan(&session.UserID, &session.Name, &session.Email, &hash, &session.Token, &banned)
	if err == sql.ErrNoRows || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return Session{}, fail(401, "Неверный логин или пароль")
	}
	if err != nil {
		return Session{}, err
	}
	if banned.Valid {
		return Session{}, fail(403, "Аккаунт заблокирован")
	}
	ok, err := hasRole(ctx, s.DB, session.UserID, role)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, fail(403, "Доступ запрещен.")
	}
	session.Role = role
	return session, nil
}

// CreateUser inserts a user, an empty USDT wallet and a role.
func (s *Service) CreateUser(ctx context.Context, actor int64, name, email, password, role string, reserve int, payouts bool) (NewUser, error) {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return NewUser{}, err
	}
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" || email == "" || password == "" || role == "" {
		return NewUser{}, invalid(map[string][]string{
			"email":    {"The email field is required."},
			"name":     {"The name field is required."},
			"password": {"The password field is required."},
			"role":     {"The role field is required."},
		})
	}
	var roleID int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM roles WHERE name = ?`, role).Scan(&roleID)
	if err == sql.ErrNoRows {
		return NewUser{}, fail(422, "Роль не найдена.")
	}
	if err != nil {
		return NewUser{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return NewUser{}, err
	}
	apiToken, err := randomToken()
	if err != nil {
		return NewUser{}, err
	}
	apkToken, err := randomToken()
	if err != nil {
		return NewUser{}, err
	}
	now := s.now()
	online := role == "Trader"
	var created NewUser
	err = s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO users (
				name, email, password, apk_access_token, api_access_token,
				avatar_uuid, avatar_style, is_online, is_payout_online, is_vip,
				referral_commission_percentage, payouts_enabled, stop_traffic,
				traffic_enabled_at, created_at, updated_at, additional_team_leader_ids
			) VALUES (?, ?, ?, ?, ?, ?, 'adventurer', ?, 0, 0, 0, ?, 0, ?, ?, ?, '[]')`,
			name, email, string(hash), apkToken, apiToken, email, online, payouts, now, now, now)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				return invalid(map[string][]string{"email": {"The email has already been taken."}})
			}
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := store.GrantRole(ctx, tx, id, roleID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO wallets (
				user_id, merchant_balance, trust_balance, reserve_balance,
				commission_balance, teamleader_balance, currency, target_reserve_amount,
				created_at, updated_at
			) VALUES (?, '0', '0', '0', '0', '0', 'usdt', ?, ?, ?)`,
			id, reserve, now, now); err != nil {
			return err
		}
		created = NewUser{ID: id, Email: email}
		return nil
	})
	return created, err
}

// PatchUser updates payout and traffic flags.
func (s *Service) PatchUser(ctx context.Context, actor, userID int64, payouts *bool, online *bool, stop *bool) error {
	return s.UpdateUser(ctx, actor, userID, UserEdit{Payouts: payouts, Online: online, Stop: stop})
}

// UserEdit is a partial admin profile update. A set flag means the cabinet sent that field.
type UserEdit struct {
	Name                  string
	NameSet               bool
	Email                 string
	EmailSet              bool
	Password              string
	PasswordSet           bool
	RoleID                int64
	RoleSet               bool
	Promo                 string
	PromoSet              bool
	CategoryID            int64
	CategorySet           bool
	CategoryClear         bool
	Banned                *bool
	Payouts               *bool
	Online                *bool
	Stop                  *bool
	Referral              string
	ReferralSet           bool
	TraderRate            string
	TraderRateSet         bool
	TraderRateClear       bool
	Reserve               int
	ReserveSet            bool
	MerchantCategories    []int64
	MerchantCategoriesSet bool
}

// UpdateUser writes the fields the admin edit form sends. Absent fields stay as they are.
func (s *Service) UpdateUser(ctx context.Context, actor, userID int64, edit UserEdit) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	var exists int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists)
	if err == sql.ErrNoRows {
		return fail(404, "Пользователь не найден")
	}
	if err != nil {
		return err
	}
	fields := map[string][]string{}
	if edit.NameSet && edit.Name == "" {
		fields["name"] = []string{"The name field is required."}
	}
	if edit.EmailSet {
		if edit.Email == "" {
			fields["email"] = []string{"The email field is required."}
		} else {
			var other int64
			err = s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ? AND id <> ?`, edit.Email, userID).Scan(&other)
			if err == nil {
				fields["email"] = []string{"The email has already been taken."}
			} else if err != sql.ErrNoRows {
				return err
			}
		}
	}
	if edit.RoleSet {
		var roleName string
		err = s.DB.QueryRowContext(ctx, `SELECT name FROM roles WHERE id = ?`, edit.RoleID).Scan(&roleName)
		if err == sql.ErrNoRows {
			fields["role_id"] = []string{"The selected role id is invalid."}
		} else if err != nil {
			return err
		} else {
			applyRolePolicy(&edit, roleName)
		}
	}
	if edit.CategorySet && !edit.CategoryClear {
		var categoryID int64
		err = s.DB.QueryRowContext(ctx, `SELECT id FROM trader_categories WHERE id = ?`, edit.CategoryID).Scan(&categoryID)
		if err == sql.ErrNoRows {
			fields["trader_category_id"] = []string{"The selected trader category id is invalid."}
		} else if err != nil {
			return err
		}
	}
	var promoID int64
	if edit.PromoSet && edit.Promo != "" {
		err = s.DB.QueryRowContext(ctx, `SELECT id FROM promo_codes WHERE code = ?`, edit.Promo).Scan(&promoID)
		if err == sql.ErrNoRows {
			fields["promo_code"] = []string{"The selected promo code is invalid."}
		} else if err != nil {
			return err
		}
	}
	if edit.ReferralSet {
		text, ok := percentText(edit.Referral)
		if !ok {
			fields["referral_commission_percentage"] = []string{"The referral commission percentage field must be a number."}
		} else {
			edit.Referral = text
		}
	}
	if edit.MerchantCategoriesSet {
		for _, categoryID := range edit.MerchantCategories {
			var found int64
			err = s.DB.QueryRowContext(ctx, `SELECT id FROM categories WHERE id = ?`, categoryID).Scan(&found)
			if err == sql.ErrNoRows {
				fields["category_ids"] = []string{"The selected category id is invalid."}
				break
			}
			if err != nil {
				return err
			}
		}
	}
	if edit.TraderRateSet && !edit.TraderRateClear {
		text, ok := percentText(edit.TraderRate)
		if !ok {
			fields["trader_commission_rate"] = []string{"The trader commission rate field must be a number."}
		} else {
			edit.TraderRate = text
		}
	}
	if len(fields) > 0 {
		return invalid(fields)
	}
	var hash []byte
	if edit.PasswordSet {
		hash, err = bcrypt.GenerateFromPassword([]byte(edit.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
	}
	now := s.now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		sets := []string{"updated_at = ?"}
		args := []any{now}
		if edit.NameSet {
			sets = append(sets, "name = ?")
			args = append(args, edit.Name)
		}
		if edit.EmailSet {
			sets = append(sets, "email = ?")
			args = append(args, edit.Email)
		}
		if edit.PasswordSet {
			sets = append(sets, "password = ?")
			args = append(args, string(hash))
		}
		if edit.CategorySet {
			sets = append(sets, "trader_category_id = ?")
			if edit.CategoryClear {
				args = append(args, nil)
			} else {
				args = append(args, edit.CategoryID)
			}
		}
		if edit.PromoSet {
			sets = append(sets, "promo_code_id = ?")
			if edit.Promo == "" {
				args = append(args, nil)
			} else {
				args = append(args, promoID)
			}
		}
		if edit.Payouts != nil {
			sets = append(sets, "payouts_enabled = ?")
			args = append(args, boolBit(*edit.Payouts))
		}
		if edit.Online != nil && (edit.Banned == nil || !*edit.Banned) {
			sets = append(sets, "is_online = ?")
			args = append(args, boolBit(*edit.Online))
		}
		if edit.Stop != nil {
			sets = append(sets, "stop_traffic = ?")
			args = append(args, boolBit(*edit.Stop))
		}
		if edit.ReferralSet {
			sets = append(sets, "referral_commission_percentage = ?")
			args = append(args, edit.Referral)
		}
		if edit.TraderRateSet {
			sets = append(sets, "trader_commission_rate = ?")
			if edit.TraderRateClear {
				args = append(args, nil)
			} else {
				args = append(args, edit.TraderRate)
			}
		}
		if edit.Banned != nil {
			if *edit.Banned {
				sets = append(sets, "banned_at = COALESCE(banned_at, ?)", "is_online = 0")
				args = append(args, now)
			} else {
				sets = append(sets, "banned_at = NULL")
			}
		}
		args = append(args, userID)
		if _, err := tx.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				return invalid(map[string][]string{"email": {"The email has already been taken."}})
			}
			return err
		}
		if edit.RoleSet {
			if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = ?`, userID); err != nil {
				return err
			}
			if err := store.GrantRole(ctx, tx, userID, edit.RoleID); err != nil {
				return err
			}
		}
		if edit.ReserveSet {
			if _, err := tx.ExecContext(ctx, `UPDATE wallets SET target_reserve_amount = ?, updated_at = ? WHERE user_id = ?`, edit.Reserve, now, userID); err != nil {
				return err
			}
		}
		if edit.MerchantCategoriesSet {
			if err := replaceMerchantCategories(ctx, tx, userID, edit.MerchantCategories, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func applyRolePolicy(edit *UserEdit, role string) {
	stop := true
	payouts := false
	switch role {
	case "Trader":
		edit.MerchantCategories = nil
		edit.MerchantCategoriesSet = true
		edit.Referral = "0.00"
		edit.ReferralSet = true
	case "Merchant":
		clearTraderFields(edit)
		edit.Payouts = &payouts
		edit.Referral = "0.00"
		edit.ReferralSet = true
	case "Team Leader":
		clearTraderFields(edit)
		edit.MerchantCategories = nil
		edit.MerchantCategoriesSet = true
		edit.Stop = &stop
		edit.Payouts = &payouts
	default:
		clearTraderFields(edit)
		edit.MerchantCategories = nil
		edit.MerchantCategoriesSet = true
		edit.Stop = &stop
		edit.Payouts = &payouts
		edit.Referral = "0.00"
		edit.ReferralSet = true
	}
}

func clearTraderFields(edit *UserEdit) {
	edit.CategorySet = true
	edit.CategoryClear = true
	edit.PromoSet = true
	edit.Promo = ""
	edit.TraderRateSet = true
	edit.TraderRateClear = true
}

func replaceMerchantCategories(ctx context.Context, tx *sql.Tx, userID int64, categoryIDs []int64, now interface{}) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM merchants WHERE user_id = ?`, userID)
	if err != nil {
		return err
	}
	var merchantIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		merchantIDs = append(merchantIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(merchantIDs) == 0 {
		if len(categoryIDs) == 0 {
			return nil
		}
		return invalid(map[string][]string{"category_ids": {"У пользователя нет проекта."}})
	}
	placeholders := make([]string, len(merchantIDs))
	args := make([]any, len(merchantIDs))
	for i, id := range merchantIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM category_merchant WHERE merchant_id IN (`+strings.Join(placeholders, ",")+`)`, args...); err != nil {
		return err
	}
	for _, merchantID := range merchantIDs {
		for _, categoryID := range categoryIDs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO category_merchant (category_id, merchant_id, created_at, updated_at)
				VALUES (?, ?, ?, ?)`, categoryID, merchantID, now, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func boolBit(value bool) int {
	if value {
		return 1
	}
	return 0
}

func percentText(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 999.99 {
		return "", false
	}
	return strconv.FormatFloat(value, 'f', 2, 64), true
}

// Deposit credits a USDT balance. Trust fills the reserve target first.
func (s *Service) Deposit(ctx context.Context, actor, userID int64, amount, balanceType string) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	value, err := money.FromPrecision(amount, "usdt")
	if err != nil {
		return invalid(map[string][]string{"amount": {"The amount field must be a number."}})
	}
	now := s.now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		switch balanceType {
		case "trust":
			return creditTrust(ctx, tx, userID, value.Units(), now)
		case "merchant":
			return creditMerchant(ctx, tx, userID, value.Units(), "deposit_by_admin", now)
		default:
			return invalid(map[string][]string{"balance_type": {"The selected balance type is invalid."}})
		}
	})
}

// ValidateMerchant stamps validated_at.
func (s *Service) ValidateMerchant(ctx context.Context, actor, merchantID int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE merchants SET validated_at = ?, updated_at = ? WHERE id = ?`, s.now(), s.now(), merchantID)
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

// SetMarketPrice stores an admin quote and holds it against the feed for ManualHold.
func (s *Service) SetMarketPrice(ctx context.Context, actor int64, currency, marketName, buy, sell string) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	if s.Quotes == nil {
		return fail(500, "redis unavailable")
	}
	buyMoney, err := money.FromPrecision(buy, currency)
	if err != nil {
		return invalid(map[string][]string{"buy_price": {"The buy price is invalid."}})
	}
	sellMoney, err := money.FromPrecision(sell, currency)
	if err != nil {
		return invalid(map[string][]string{"sell_price": {"The sell price is invalid."}})
	}
	if marketName == "" {
		marketName = "bybit"
	}
	return s.Quotes.Put(ctx, currency, marketName, market.Quote{
		BuyUnits:  buyMoney.Units(),
		SellUnits: sellMoney.Units(),
		HoldUntil: s.now().Add(market.ManualHold),
	})
}

func (s *Service) requireRole(ctx context.Context, userID int64, role string) error {
	ok, err := hasRole(ctx, s.DB, userID, role)
	if err != nil {
		return err
	}
	if !ok {
		return plain(403, "This action is unauthorized.")
	}
	return nil
}

func hasRole(ctx context.Context, db *sql.DB, userID int64, role string) (bool, error) {
	return store.HasRole(ctx, db, userID, role)
}
