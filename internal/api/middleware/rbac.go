package middleware

import (
	"net/http"

	"github.com/labstack/echo/v5"
	apiauth "github.com/paintingpromisesss/deadliner_bot/internal/api/auth"
	responses "github.com/paintingpromisesss/deadliner_bot/internal/api/transport/responses"
)

func RequireMember(authorizer *apiauth.MembershipAuthorizer) echo.MiddlewareFunc {
	return requireAccess(authorizer, false)
}

func RequireAdmin(authorizer *apiauth.MembershipAuthorizer) echo.MiddlewareFunc {
	return requireAccess(authorizer, true)
}

func requireAccess(authorizer *apiauth.MembershipAuthorizer, adminOnly bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if authorizer == nil {
				return responses.JSONErrorMessage(c, http.StatusServiceUnavailable, "membership authorizer is not configured")
			}

			userID, ok := UserIDFromContext(c)
			if !ok || userID == 0 {
				return responses.JSONErrorMessage(c, http.StatusUnauthorized, "unauthorized")
			}

			chatID, ok := ChatIDFromContext(c)
			if !ok || chatID == 0 {
				initData := c.Request().Header.Get(HeaderTelegramInitData)
				resolvedChatID, err := apiauth.ResolveChatID(c, initData)
				if err != nil {
					return responses.JSONError(c, http.StatusBadRequest, err)
				}
				if resolvedChatID == 0 {
					return responses.JSONError(c, http.StatusBadRequest, apiauth.ErrMissingChatID)
				}
				chatID = resolvedChatID
				c.Set(contextKeyAuthChatID, chatID)
			}

			status, err := authorizer.GetChatMemberType(c.Request().Context(), chatID, userID)
			if err != nil {
				return responses.JSONErrorMessage(c, http.StatusBadGateway, "failed to check chat membership")
			}

			if !apiauth.IsMemberStatus(status) {
				return responses.JSONErrorMessage(c, http.StatusForbidden, "membership required")
			}
			if adminOnly && !apiauth.IsAdminStatus(status) {
				return responses.JSONErrorMessage(c, http.StatusForbidden, "admin required")
			}

			return next(c)
		}
	}
}
