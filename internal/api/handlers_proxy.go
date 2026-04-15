package api

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/flowcase/flowcase/internal/infra/proxy"
	"github.com/labstack/echo/v4"
)

func (h *Handlers) ProxyDesktop(c echo.Context) error {
	instanceID := c.Param("instanceId")
	claims := GetClaims(c)
	if claims == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	if h.svc.Docker == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "docker unavailable"})
	}

	route := h.svc.Docker.GetRoute(instanceID)
	if route == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found or not running"})
	}

	// Verify the user owns this instance or is admin
	instance, err := h.svc.Store.GetInstance(c.Request().Context(), route.InstanceID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instance not found"})
	}

	if instance.UserID != claims.UserID {
		hasAdmin := false
		for _, p := range claims.Permissions {
			if p == "edit_instances" {
				hasAdmin = true
				break
			}
		}
		if !hasAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "not your instance"})
		}
	}

	// Build basic auth for upstream
	basicAuth := base64.StdEncoding.EncodeToString([]byte("flowcase_user:" + claims.UserID.String()))

	// Determine sub-path after /desktop/:instanceId/
	subPath := c.Request().URL.Path
	prefix := "/desktop/" + instanceID

	// Route based on sub-path
	switch {
	case strings.Contains(subPath, "/vnc/websockify"):
		wsURL := route.Address + "/websockify/"
		proxy.WebSocketProxy(wsURL, basicAuth).ServeHTTP(c.Response(), c.Request())
		return nil

	case strings.Contains(subPath, "/audio/"):
		audioAddr := strings.Replace(route.Address, ":6901", ":4901", 1)
		proxy.WebSocketProxy(audioAddr, basicAuth).ServeHTTP(c.Response(), c.Request())
		return nil

	case strings.Contains(subPath, "/uploads/"):
		uploadAddr := strings.Replace(route.Address, ":6901", ":4902", 1)
		stripPath := prefix + "/uploads"
		proxy.HTTPProxy(uploadAddr, stripPath, basicAuth).ServeHTTP(c.Response(), c.Request())
		return nil

	case strings.Contains(subPath, "/vnc/"):
		stripPath := prefix + "/vnc"
		proxy.HTTPProxy(route.Address, stripPath, basicAuth).ServeHTTP(c.Response(), c.Request())
		return nil

	default:
		return c.JSON(http.StatusNotFound, map[string]string{"error": "unknown proxy path"})
	}
}
