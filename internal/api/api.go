package api

import (
	"net/http"
	"time"

	tgbot "github.com/go-telegram/bot"
	apiserver "github.com/paintingpromisesss/deadliner_bot/internal/api/server"
)

// NewServer creates configured HTTP server for API and static content.
func NewServer(addr string, readTimeout, writeTimeout time.Duration, webDir string, botToken string, botClient *tgbot.Bot, memberCacheTTL time.Duration) *http.Server {
	return apiserver.NewServer(addr, readTimeout, writeTimeout, webDir, botToken, botClient, memberCacheTTL)
}
