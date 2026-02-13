package api

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// NewServer creates an HTTP server that serves static web and a health endpoint.
func NewServer(addr string, readTimeout, writeTimeout time.Duration, webDir string) *http.Server {
	e := echo.New()
	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	e.GET("/health", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	e.Static("/", webDir)

	return &http.Server{
		Addr:         addr,
		Handler:      e,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}
