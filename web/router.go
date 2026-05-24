package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// NewRouter constructs the echo HTTP server with all routes wired up.
// frontendFS is the embedded SPA bundle (typically frontend/dist via go:embed).
func NewRouter(frontendFS embed.FS, frontendRoot string) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(middleware.Recover())
	e.Use(middleware.Logger())
	e.Use(middleware.BodyLimit("5M"))

	// --- Public API ---
	api := e.Group("/api")
	api.GET("/health", handleHealth)
	api.POST("/auth/login", handleLogin)
	api.POST("/auth/logout", handleLogout)

	// --- Authenticated API ---
	priv := api.Group("", RequireAuth)
	priv.GET("/auth/me", handleMe)
	priv.POST("/auth/invite", handleInvite)
	priv.POST("/search", handleSearch)
	priv.POST("/metadata", handleMetadata)
	priv.POST("/download", handleDownload)
	priv.GET("/history", handleHistoryList)
	priv.GET("/events/queue", handleQueueEvents)

	// --- Static frontend (SPA fallback) ---
	mountSPA(e, frontendFS, frontendRoot)

	return e
}

// mountSPA serves the embedded SPA. Any request that isn't /api/* and doesn't match
// a real file falls back to index.html so the client-side router can handle it.
func mountSPA(e *echo.Echo, embedded embed.FS, root string) {
	sub, err := fs.Sub(embedded, root)
	if err != nil {
		e.Logger.Warnf("embedded frontend not available (%v) — API-only mode", err)
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	e.GET("/*", func(c echo.Context) error {
		path := c.Request().URL.Path
		if strings.HasPrefix(path, "/api/") {
			return echo.ErrNotFound
		}
		// If the requested file exists in the embed, serve it; otherwise serve index.html.
		trimmed := strings.TrimPrefix(path, "/")
		if trimmed == "" {
			trimmed = "index.html"
		}
		if _, err := fs.Stat(sub, trimmed); err != nil {
			c.Request().URL.Path = "/"
		}
		fileServer.ServeHTTP(c.Response(), c.Request())
		return nil
	})
}
