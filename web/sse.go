package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/afkarxyz/SpotiFLAC/backend"
	"github.com/labstack/echo/v4"
)

// handleQueueEvents streams per-user queue/progress updates as Server-Sent Events.
// The browser keeps the connection open and renders updates in real time.
func handleQueueEvents(c echo.Context) error {
	username := CurrentUser(c)

	resp := c.Response()
	resp.Header().Set(echo.HeaderContentType, "text/event-stream")
	resp.Header().Set("Cache-Control", "no-cache")
	resp.Header().Set("Connection", "keep-alive")
	resp.Header().Set("X-Accel-Buffering", "no") // disable nginx/Caddy buffering
	resp.WriteHeader(http.StatusOK)

	events, cancel := backend.Subscribe(username)
	defer cancel()

	// Emit an initial comment so the connection settles quickly behind proxies.
	if _, err := fmt.Fprintf(resp, ": connected at %d\n\n", time.Now().Unix()); err != nil {
		return nil
	}
	resp.Flush()

	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	notifyClose := c.Request().Context().Done()

	for {
		select {
		case <-notifyClose:
			return nil
		case <-pingTicker.C:
			if _, err := fmt.Fprintf(resp, ": ping %d\n\n", time.Now().Unix()); err != nil {
				return nil
			}
			resp.Flush()
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(resp, "event: %s\ndata: %s\n\n", ev.Kind, payload); err != nil {
				return nil
			}
			resp.Flush()
		}
	}
}
