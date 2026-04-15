package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

var Bus = NewEventBus()

func (h *Handlers) SSE(c echo.Context) error {
	claims := GetClaims(c)
	if claims == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().Header().Set("X-Accel-Buffering", "no")
	c.Response().WriteHeader(http.StatusOK)

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
	}

	subID := uuid.New().String()
	ch := Bus.Subscribe(subID)
	defer Bus.Unsubscribe(subID)

	// Send initial connected event
	fmt.Fprintf(c.Response().Writer, "event: connected\ndata: {\"status\":\"ok\"}\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-c.Request().Context().Done():
			return nil
		case event, ok := <-ch:
			if !ok {
				return nil
			}
			fmt.Fprint(c.Response().Writer, event.SSEFormat())
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(c.Response().Writer, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
