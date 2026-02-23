package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	apiauth "github.com/paintingpromisesss/deadliner_bot/internal/api/auth"
	responses "github.com/paintingpromisesss/deadliner_bot/internal/api/transport/responses"
)

const (
	HeaderTelegramInitData = "X-Telegram-Init-Data"

	contextKeyAuthUserID = "auth_user_id"
	contextKeyAuthChatID = "auth_chat_id"
)

// AuthMiddleware validates Telegram Mini App init data and stores user/chat context.
func AuthMiddleware(botToken string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			initData := strings.TrimSpace(c.Request().Header.Get(HeaderTelegramInitData))
			if initData == "" {
				return responses.JSONError(c, http.StatusUnauthorized, apiauth.ErrMissingInitData)
			}

			payload, err := apiauth.ValidateInitData(initData, botToken)
			if err != nil {
				return responses.JSONError(c, http.StatusUnauthorized, err)
			}

			chatID, _ := apiauth.ResolveChatID(c, initData)
			payload.ChatID = chatID

			c.Set(contextKeyAuthUserID, payload.UserID)
			if payload.ChatID != 0 {
				c.Set(contextKeyAuthChatID, payload.ChatID)
			}

			return next(c)
		}
	}
}

// UserIDFromContext returns authenticated Telegram user id.
func UserIDFromContext(c *echo.Context) (int64, bool) {
	value := c.Get(contextKeyAuthUserID)
	userID, ok := value.(int64)
	return userID, ok && userID != 0
}

// ChatIDFromContext returns resolved chat id if present.
func ChatIDFromContext(c *echo.Context) (int64, bool) {
	value := c.Get(contextKeyAuthChatID)
	chatID, ok := value.(int64)
	return chatID, ok && chatID != 0
}
