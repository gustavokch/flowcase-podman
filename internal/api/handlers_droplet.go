package api

import (
	"errors"
	"net/http"

	"github.com/flowcase/flowcase/internal/app"
	"github.com/flowcase/flowcase/internal/domain"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func (h *Handlers) ListDroplets(c echo.Context) error {
	claims := GetClaims(c)
	droplets, err := h.svc.Droplets.ListForUser(c.Request().Context(), claims.UserID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if droplets == nil {
		droplets = []domain.Droplet{}
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "droplets": droplets})
}

func (h *Handlers) GetDroplet(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}

	d, err := h.svc.Droplets.Get(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, d)
}

func (h *Handlers) CreateDroplet(c echo.Context) error {
	var req app.CreateDropletRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	if req.DisplayName == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "display_name required"})
	}

	d, err := h.svc.Droplets.Create(c.Request().Context(), req)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid droplet type"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusCreated, d)
}

func (h *Handlers) UpdateDroplet(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}

	var req app.UpdateDropletRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	req.ID = id

	if err := h.svc.Droplets.Update(c.Request().Context(), req); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handlers) DeleteDroplet(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}

	if err := h.svc.Droplets.Delete(c.Request().Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) ListInstances(c echo.Context) error {
	claims := GetClaims(c)
	instances, err := h.svc.Store.ListInstancesByUser(c.Request().Context(), claims.UserID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	type instanceResp struct {
		domain.DropletInstance
		Droplet *domain.Droplet `json:"droplet,omitempty"`
	}

	var resp []instanceResp
	for _, inst := range instances {
		ir := instanceResp{DropletInstance: inst}
		if d, err := h.svc.Store.GetDroplet(c.Request().Context(), inst.DropletID); err == nil {
			ir.Droplet = d
		}
		resp = append(resp, ir)
	}

	if resp == nil {
		resp = []instanceResp{}
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "instances": resp})
}

func (h *Handlers) CreateInstance(c echo.Context) error {
	claims := GetClaims(c)
	var req struct {
		DropletID  string `json:"droplet_id"`
		Resolution string `json:"resolution"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}

	dropletID, err := uuid.Parse(req.DropletID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid droplet_id"})
	}

	instance, err := h.svc.Instances.RequestInstance(c.Request().Context(), app.RequestInstanceOpts{
		DropletID:  dropletID,
		UserID:     claims.UserID,
		Resolution: req.Resolution,
	})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, domain.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, domain.ErrDockerUnavailable):
			status = http.StatusServiceUnavailable
		case errors.Is(err, domain.ErrImageNotFound):
			status = http.StatusBadRequest
		case errors.Is(err, domain.ErrInsufficientRes):
			status = http.StatusBadRequest
		}
		return c.JSON(status, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusCreated, map[string]any{"success": true, "instance_id": instance.ID.String()})
}

func (h *Handlers) DestroyInstance(c echo.Context) error {
	claims := GetClaims(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}

	instance, err := h.svc.Store.GetInstance(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	if instance.UserID != claims.UserID {
		hasAdmin := false
		for _, p := range claims.Permissions {
			if p == domain.PermEditInstances {
				hasAdmin = true
				break
			}
		}
		if !hasAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "not your instance"})
		}
	}

	if err := h.svc.Instances.DestroyInstance(c.Request().Context(), id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	return c.JSON(http.StatusOK, map[string]string{"success": "true"})
}
