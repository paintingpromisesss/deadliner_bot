package transport

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// SuccessResponse is a unified payload for simple successful actions.
type SuccessResponse struct {
	Status string `json:"status"`
}

// AuthPingResponse returns currently resolved auth context.
type AuthPingResponse struct {
	UserID int64 `json:"user_id"`
	ChatID int64 `json:"chat_id"`
}

// NewSuccessResponse builds a default success payload.
func NewSuccessResponse() SuccessResponse {
	return SuccessResponse{Status: "ok"}
}

// NewAuthPingResponse builds auth ping payload.
func NewAuthPingResponse(userID, chatID int64) AuthPingResponse {
	return AuthPingResponse{
		UserID: userID,
		ChatID: chatID,
	}
}

// JSONOK writes a default successful response.
func JSONOK(c *echo.Context) error {
	return c.JSON(http.StatusOK, NewSuccessResponse())
}
