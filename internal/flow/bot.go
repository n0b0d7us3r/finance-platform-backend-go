package flow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/mail"
	"net/url"
	"strings"

	"finance-platform/go/internal/money"
	"finance-platform/go/internal/store"

	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"
)

// BotUser is a trader created from the bot.
type BotUser struct {
	ID       int64
	Name     string
	Email    string
	Avatar   string
	Password string
}

// BotBalance is the add-balance result, already formatted for the bot response.
type BotBalance struct {
	UserID  int64
	Name    string
	Email   string
	Type    string
	Added   string
	Balance string
}

// RegisterTrader creates an offline trader with a $1000 reserve.
// The password is returned once, the same way the bot client receives it.
func (s *Service) RegisterTrader(ctx context.Context, telegramID, username string) (BotUser, error) {
	fields := map[string][]string{}
	telegramID = strings.TrimSpace(telegramID)
	username = strings.TrimSpace(username)
	if telegramID == "" {
		fields["id"] = []string{"The id field is required."}
	} else if len(telegramID) > 255 {
		fields["id"] = []string{"The id field must not be greater than 255 characters."}
	}
	if username == "" {
		fields["username"] = []string{"The username field is required."}
	} else if len(username) > 255 {
		fields["username"] = []string{"The username field must not be greater than 255 characters."}
	}
	if len(fields) > 0 {
		return BotUser{}, invalid(fields)
	}
	email := telegramID + "@gmail.com"
	password := username + telegramID
	var exists int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM users WHERE email = ? LIMIT 1`, email).Scan(&exists)
	if err == nil {
		return BotUser{}, fail(400, "Пользователь с таким ID уже существует.")
	}
	if err != sql.ErrNoRows {
		return BotUser{}, err
	}
	var roleID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM roles WHERE name = 'Trader'`).Scan(&roleID); err != nil {
		return BotUser{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return BotUser{}, err
	}
	apiToken, err := randomString(60)
	if err != nil {
		return BotUser{}, err
	}
	apkToken, err := randomString(60)
	if err != nil {
		return BotUser{}, err
	}
	now := s.now()
	var created BotUser
	err = s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO users (
				name, email, password, apk_access_token, api_access_token,
				avatar_uuid, avatar_style, is_online, is_payout_online, is_vip,
				referral_commission_percentage, trader_commission_rate, payouts_enabled, stop_traffic,
				created_at, updated_at, additional_team_leader_ids
			) VALUES (?, ?, ?, ?, ?, ?, 'adventurer', 0, 0, 0, 0, 0, 1, 0, ?, ?, '[]')`,
			username, email, string(hash), apkToken, apiToken, email, now, now)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				return fail(400, "Пользователь с таким ID уже существует.")
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
				commission_balance, teamleader_balance, currency, created_at, updated_at
			) VALUES (?, '0', '0', '100000', '0', '0', 'usdt', ?, ?)`,
			id, now, now); err != nil {
			return err
		}
		created = BotUser{
			ID:       id,
			Name:     username,
			Email:    email,
			Avatar:   "https://api.dicebear1.com/9.x/adventurer/svg?seed=" + url.QueryEscape(email),
			Password: password,
		}
		return nil
	})
	return created, err
}

// AddBotBalance adds a decimal amount to one wallet column and writes the ledger row.
func (s *Service) AddBotBalance(ctx context.Context, email, amount, balanceType string) (BotBalance, error) {
	fields := map[string][]string{}
	email = strings.TrimSpace(email)
	amount = strings.TrimSpace(amount)
	balanceType = strings.TrimSpace(balanceType)
	if email == "" {
		fields["email"] = []string{"The email field is required."}
	} else if _, err := mail.ParseAddress(email); err != nil || strings.ContainsAny(email, " <>") {
		fields["email"] = []string{"The email field must be a valid email address."}
	}
	column, known := botBalanceColumns[balanceType]
	if balanceType == "" {
		fields["balance_type"] = []string{"The balance type field is required."}
	} else if !known {
		fields["balance_type"] = []string{"The selected balance type is invalid."}
	}
	if amount == "" {
		fields["amount"] = []string{"The amount field is required."}
	}
	var value money.Money
	if amount != "" {
		parsed, err := decimal.NewFromString(amount)
		if err != nil {
			fields["amount"] = []string{"The amount field must be a number."}
		} else if parsed.LessThan(decimal.RequireFromString("0.01")) {
			fields["amount"] = []string{"The amount field must be at least 0.01."}
		} else {
			value, err = money.FromPrecision(amount, "usdt")
			if err != nil {
				fields["amount"] = []string{"The amount field must be a number."}
			}
		}
	}
	if len(fields) > 0 {
		return BotBalance{}, invalid(fields)
	}
	var userID int64
	var name string
	err := s.DB.QueryRowContext(ctx, `SELECT id, name FROM users WHERE email = ?`, email).Scan(&userID, &name)
	if err == sql.ErrNoRows {
		return BotBalance{}, invalid(map[string][]string{"email": {"The selected email is invalid."}})
	}
	if err != nil {
		return BotBalance{}, err
	}
	now := s.now()
	var next string
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var walletID int64
		var current sql.NullString
		query := `SELECT id, ` + column + ` FROM wallets WHERE user_id = ? FOR UPDATE`
		err := tx.QueryRowContext(ctx, query, userID).Scan(&walletID, &current)
		if err == sql.ErrNoRows {
			if _, err = tx.ExecContext(ctx, `
				INSERT INTO wallets (
					user_id, merchant_balance, trust_balance, reserve_balance,
					commission_balance, teamleader_balance, currency, created_at, updated_at
				) VALUES (?, '0', '0', '0', '0', '0', 'usdt', ?, ?)`, userID, now, now); err != nil {
				return err
			}
			err = tx.QueryRowContext(ctx, query, userID).Scan(&walletID, &current)
		}
		if err != nil {
			return err
		}
		have, err := money.FromUnits(zero(current.String), "usdt")
		if err != nil {
			return err
		}
		sum, err := have.AddMoney(value)
		if err != nil {
			return err
		}
		next = sum.Units()
		if _, err = tx.ExecContext(ctx, `UPDATE wallets SET `+column+` = ?, updated_at = ? WHERE id = ?`, next, now, walletID); err != nil {
			return err
		}
		return postEntry(ctx, tx, walletID, value.Units(), "in", "deposit_by_admin", balanceType, now)
	})
	if err != nil {
		return BotBalance{}, err
	}
	added, err := dollar(value)
	if err != nil {
		return BotBalance{}, err
	}
	balance, err := money.FromUnits(next, "usdt")
	if err != nil {
		return BotBalance{}, err
	}
	shown, err := dollar(balance)
	if err != nil {
		return BotBalance{}, err
	}
	return BotBalance{
		UserID:  userID,
		Name:    name,
		Email:   email,
		Type:    balanceType,
		Added:   added,
		Balance: shown,
	}, nil
}

func dollar(value money.Money) (string, error) {
	precision, err := value.Precision()
	if err != nil {
		return "", err
	}
	return "$" + precision, nil
}

func randomString(n int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}

var botBalanceColumns = map[string]string{
	"merchant":   "merchant_balance",
	"trust":      "trust_balance",
	"reserve":    "reserve_balance",
	"commission": "commission_balance",
	"teamleader": "teamleader_balance",
}
