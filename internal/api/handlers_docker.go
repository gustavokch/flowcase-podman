package api

import (
	"net/http"

	dkr "github.com/flowcase/flowcase/internal/infra/docker"
	"github.com/labstack/echo/v4"
)

func (h *Handlers) ListNetworks(c echo.Context) error {
	if h.svc.Docker == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "docker unavailable"})
	}
	networks, err := h.svc.Docker.ListNetworks(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "networks": networks})
}

func (h *Handlers) ListImageStatus(c echo.Context) error {
	if h.svc.Docker == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "docker unavailable"})
	}

	droplets, err := h.svc.Store.ListDroplets(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	var required []dkr.RequiredImage
	for _, d := range droplets {
		if d.DockerImage == "" {
			continue
		}
		fullImage := d.DockerImage
		if d.DockerRegistry != "" && d.DockerRegistry != "docker.io" {
			fullImage = d.DockerRegistry + "/" + d.DockerImage
		}
		required = append(required, dkr.RequiredImage{
			ID:          d.ID.String(),
			Name:        d.DisplayName,
			Description: "Droplet: " + d.DisplayName,
			FullImage:   fullImage,
		})
	}

	status := h.svc.Docker.GetImagesStatus(c.Request().Context(), required)
	return c.JSON(http.StatusOK, map[string]any{"success": true, "images": status})
}

func (h *Handlers) PullImage(c echo.Context) error {
	if h.svc.Docker == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "docker unavailable"})
	}

	var req struct {
		Image string `json:"image"`
	}
	if err := c.Bind(&req); err != nil || req.Image == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "image required"})
	}

	if err := h.svc.Docker.PullImage(c.Request().Context(), req.Image); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "pulled", "image": req.Image})
}
