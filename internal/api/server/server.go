package server

import (
	"net/http"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/labstack/echo/v5"
	echomw "github.com/labstack/echo/v5/middleware"
	apiauth "github.com/paintingpromisesss/deadliner_bot/internal/api/auth"
	apimw "github.com/paintingpromisesss/deadliner_bot/internal/api/middleware"
	responses "github.com/paintingpromisesss/deadliner_bot/internal/api/transport/responses"
)

// NewServer creates an HTTP server that serves static web and basic API endpoints.
func NewServer(addr string, readTimeout, writeTimeout time.Duration, webDir string, botToken string, botClient *tgbot.Bot, memberCacheTTL time.Duration) *http.Server {
	e := echo.New()
	e.Use(echomw.RequestLogger())
	e.Use(echomw.Recover())

	e.GET("/health", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	authz := apiauth.NewMembershipAuthorizer(botClient, memberCacheTTL)
	api := e.Group("/api", apimw.AuthMiddleware(botToken))

	api.GET("/auth/ping", func(c *echo.Context) error {
		userID, _ := apimw.UserIDFromContext(c)
		chatID, _ := apimw.ChatIDFromContext(c)
		return c.JSON(http.StatusOK, responses.NewAuthPingResponse(userID, chatID))
	})
	api.GET("/member/ping", func(c *echo.Context) error {
		return responses.JSONOK(c)
	}, apimw.RequireMember(authz))
	api.GET("/admin/ping", func(c *echo.Context) error {
		return responses.JSONOK(c)
	}, apimw.RequireAdmin(authz))

	e.Static("/", webDir)

	return &http.Server{
		Addr:         addr,
		Handler:      e,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}
