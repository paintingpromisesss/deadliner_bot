package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
)

var (
	ErrMissingInitData = errors.New("missing init data")
	ErrInvalidInitData = errors.New("invalid init data")
	ErrMissingUserID   = errors.New("missing user id in init data")
	ErrMissingChatID   = errors.New("chat_id is required")
)

type telegramInitUser struct {
	ID int64 `json:"id"`
}

type Payload struct {
	UserID int64
	ChatID int64
}

func ValidateInitData(initData, botToken string) (Payload, error) {
	if strings.TrimSpace(botToken) == "" {
		return Payload{}, fmt.Errorf("%w: bot token is empty", ErrInvalidInitData)
	}

	values, err := url.ParseQuery(initData)
	if err != nil {
		return Payload{}, fmt.Errorf("%w: parse error", ErrInvalidInitData)
	}

	hash := values.Get("hash")
	if hash == "" {
		return Payload{}, fmt.Errorf("%w: hash is missing", ErrInvalidInitData)
	}
	values.Del("hash")

	dataCheckString := buildDataCheckString(values)
	secretKey := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	expectedHash := hmacSHA256(secretKey, []byte(dataCheckString))

	decodedHash, err := hex.DecodeString(hash)
	if err != nil {
		return Payload{}, fmt.Errorf("%w: hash decode failed", ErrInvalidInitData)
	}

	if !hmac.Equal(decodedHash, expectedHash) {
		return Payload{}, fmt.Errorf("%w: hash mismatch", ErrInvalidInitData)
	}

	userID, err := extractUserID(values)
	if err != nil {
		return Payload{}, err
	}

	return Payload{UserID: userID}, nil
}

func extractUserID(values url.Values) (int64, error) {
	parseUser := func(raw string) (int64, error) {
		if strings.TrimSpace(raw) == "" {
			return 0, nil
		}

		var user telegramInitUser
		if err := json.Unmarshal([]byte(raw), &user); err != nil {
			return 0, fmt.Errorf("%w: invalid user payload", ErrInvalidInitData)
		}
		return user.ID, nil
	}

	userID, err := parseUser(values.Get("user"))
	if err != nil {
		return 0, err
	}
	if userID == 0 {
		userID, err = parseUser(values.Get("receiver"))
		if err != nil {
			return 0, err
		}
	}
	if userID == 0 {
		return 0, ErrMissingUserID
	}

	return userID, nil
}

func ResolveChatID(c *echo.Context, initData string) (int64, error) {
	if chatID, err := parseInt64(strings.TrimSpace(c.QueryParam("chat_id"))); err != nil {
		return 0, fmt.Errorf("%w: invalid query chat_id", ErrMissingChatID)
	} else if chatID != 0 {
		return chatID, nil
	}

	bodyChatID, err := readChatIDFromBody(c)
	if err != nil {
		return 0, err
	}
	if bodyChatID != 0 {
		return bodyChatID, nil
	}

	values, err := url.ParseQuery(initData)
	if err != nil {
		return 0, nil
	}

	chatID, err := parseInt64(values.Get("start_param"))
	if err != nil {
		return 0, nil
	}

	return chatID, nil
}

func readChatIDFromBody(c *echo.Context) (int64, error) {
	req := c.Request()
	if req.Body == nil {
		return 0, nil
	}
	if req.Method == http.MethodGet || req.Method == http.MethodDelete {
		return 0, nil
	}
	if !strings.Contains(strings.ToLower(req.Header.Get(echo.HeaderContentType)), echo.MIMEApplicationJSON) {
		return 0, nil
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return 0, fmt.Errorf("%w: cannot read body", ErrMissingChatID)
	}
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	if len(bytes.TrimSpace(bodyBytes)) == 0 {
		return 0, nil
	}

	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return 0, nil
	}

	raw, ok := body["chat_id"]
	if !ok {
		return 0, nil
	}

	switch v := raw.(type) {
	case float64:
		return int64(v), nil
	case string:
		return parseInt64(v)
	default:
		return 0, fmt.Errorf("%w: invalid body chat_id type", ErrMissingChatID)
	}
}

func parseInt64(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func buildDataCheckString(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+values.Get(key))
	}

	return strings.Join(pairs, "\n")
}

func hmacSHA256(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}
