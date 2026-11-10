package store

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

type TelegramSettings struct {
	Enabled  bool   `json:"enabled"`
	ChatID   string `json:"chat_id"`
	Token    string `json:"-"`
	TokenSet bool   `json:"token_set"`
}
type TelegramChanges struct {
	Enabled    bool   `json:"enabled"`
	ChatID     string `json:"chat_id"`
	Token      string `json:"token"`
	ClearToken bool   `json:"clear_token"`
}

var ErrInvalidTelegram = errors.New("invalid Telegram settings")
var tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var chatPattern = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z0-9_]+)$`)

func (s *Store) Telegram(ctx context.Context, token, chat string) (TelegramSettings, error) {
	v := TelegramSettings{Enabled: token != "" && chat != "", Token: token, ChatID: chat, TokenSet: token != ""}
	var encrypted string
	e := s.Pool.QueryRow(ctx, "SELECT enabled,chat_id,token FROM telegram_settings WHERE id=1").Scan(&v.Enabled, &v.ChatID, &encrypted)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, nil
	}
	if e != nil {
		return TelegramSettings{}, e
	}
	v.Token = ""
	if encrypted != "" {
		v.Token, e = s.cipher.Decrypt(encrypted)
	}
	v.TokenSet = v.Token != ""
	return v, e
}
func (s *Store) SaveTelegram(ctx context.Context, v TelegramChanges, fallbackToken, fallbackChat string) (TelegramSettings, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return TelegramSettings{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(712039442)"); e != nil {
		return TelegramSettings{}, e
	}
	var encrypted string
	e = tx.QueryRow(ctx, "SELECT token FROM telegram_settings WHERE id=1").Scan(&encrypted)
	token := fallbackToken
	if e == nil {
		token = ""
		if encrypted != "" {
			token, e = s.cipher.Decrypt(encrypted)
		}
	} else if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	if e != nil {
		return TelegramSettings{}, e
	}
	if v.ClearToken {
		token = ""
	} else if strings.TrimSpace(v.Token) != "" {
		token = strings.TrimSpace(v.Token)
	}
	chat := strings.TrimSpace(v.ChatID)
	if (token != "" && (len(token) > 256 || !tokenPattern.MatchString(token))) || (chat != "" && (len(chat) > 128 || !chatPattern.MatchString(chat))) || (v.Enabled && (token == "" || chat == "")) {
		return TelegramSettings{}, ErrInvalidTelegram
	}
	encrypted = ""
	if token != "" {
		encrypted, e = s.cipher.Encrypt(token)
		if e != nil {
			return TelegramSettings{}, e
		}
	}
	_, e = tx.Exec(ctx, "INSERT INTO telegram_settings(id,enabled,chat_id,token) VALUES(1,$1,$2,$3) ON CONFLICT(id) DO UPDATE SET enabled=$1,chat_id=$2,token=$3", v.Enabled, chat, encrypted)
	if e != nil {
		return TelegramSettings{}, e
	}
	return TelegramSettings{Enabled: v.Enabled, ChatID: chat, Token: token, TokenSet: token != ""}, tx.Commit(ctx)
}
