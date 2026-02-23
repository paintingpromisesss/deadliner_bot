package transport

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// ErrorResponse is a unified API error payload.
type ErrorResponse struct {
	Error string `json:"error"`
}

// NewErrorResponse constructs ErrorResponse from message.
func NewErrorResponse(message string) ErrorResponse {
	return ErrorResponse{Error: message}
}

// NewErrorResponseFromErr constructs ErrorResponse from error.
func NewErrorResponseFromErr(err error) ErrorResponse {
	if err == nil {
		return NewErrorResponse(http.StatusText(http.StatusInternalServerError))
	}
	return NewErrorResponse(err.Error())
}

// JSONError writes a JSON error response from error.
func JSONError(c *echo.Context, status int, err error) error {
	return c.JSON(status, NewErrorResponseFromErr(err))
}

// JSONErrorMessage writes a JSON error response from plain message.
func JSONErrorMessage(c *echo.Context, status int, message string) error {
	return c.JSON(status, NewErrorResponse(message))
}
