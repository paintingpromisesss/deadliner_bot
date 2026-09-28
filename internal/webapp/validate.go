// Package webapp проверяет initData Telegram Mini Apps по официальному
// алгоритму (спека §5.1): HMAC-подпись на ключе WebAppData + свежесть auth_date.
package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// User — данные пользователя из поля user в initData.
type User struct {
	TelegramID int64
	Username   string
	FirstName  string
}

// InitData — результат успешной валидации.
type InitData struct {
	User     User
	AuthDate time.Time
}

// tgUser — JSON-форма поля user из initData Telegram.
type tgUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

// ValidateInitData проверяет подпись и свежесть query-строки initData
// (спека §5.1, официальный алгоритм Telegram):
//
//	secret_key        = HMAC_SHA256(key="WebAppData", msg=botToken)
//	data_check_string = пары без hash, отсортированные по ключу, "key=value" через "\n"
//	hash              == hex(HMAC_SHA256(secret_key, data_check_string))  (constant-time)
//	now - auth_date   <= maxAge
//
// Значения пар используются URL-декодированными (как их отдаёт url.ParseQuery).
// Любая ошибка заворачивает domain.ErrForbidden.
func ValidateInitData(raw, botToken string, now time.Time, maxAge time.Duration) (*InitData, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w: empty initData", domain.ErrForbidden)
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: parse initData: %v", domain.ErrForbidden, err)
	}

	hash := values.Get("hash")
	if hash == "" {
		return nil, fmt.Errorf("%w: initData missing hash", domain.ErrForbidden)
	}
	wantMAC, err := hex.DecodeString(hash)
	if err != nil {
		return nil, fmt.Errorf("%w: initData hash not hex: %v", domain.ErrForbidden, err)
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var checkString string
	for i, k := range keys {
		if i > 0 {
			checkString += "\n"
		}
		checkString += k + "=" + values.Get(k)
	}

	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(checkString))
	if subtle.ConstantTimeCompare(mac.Sum(nil), wantMAC) != 1 {
		return nil, fmt.Errorf("%w: initData signature mismatch", domain.ErrForbidden)
	}

	authDateStr := values.Get("auth_date")
	authDateUnix, err := strconv.ParseInt(authDateStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: initData auth_date invalid: %v", domain.ErrForbidden, err)
	}
	authDate := time.Unix(authDateUnix, 0)
	if now.Sub(authDate) > maxAge {
		return nil, fmt.Errorf("%w: initData auth_date too old (%s > %s)",
			domain.ErrForbidden, now.Sub(authDate), maxAge)
	}

	var u tgUser
	if err := json.Unmarshal([]byte(values.Get("user")), &u); err != nil {
		return nil, fmt.Errorf("%w: initData user field: %v", domain.ErrForbidden, err)
	}
	if u.ID == 0 {
		return nil, fmt.Errorf("%w: initData user missing id", domain.ErrForbidden)
	}
	if u.FirstName == "" && u.Username == "" {
		return nil, fmt.Errorf("%w: initData user missing first_name/username", domain.ErrForbidden)
	}

	return &InitData{
		User: User{
			TelegramID: u.ID,
			Username:   u.Username,
			FirstName:  u.FirstName,
		},
		AuthDate: authDate,
	}, nil
}
