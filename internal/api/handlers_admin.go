package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/flowcase/flowcase/internal/app"
	"github.com/flowcase/flowcase/internal/domain"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func (h *Handlers) GetSystemInfo(c echo.Context) error {
	info, err := h.svc.Admin.GetSystemInfo(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, info)
}

func (h *Handlers) ListUsers(c echo.Context) error {
	users, err := h.svc.Users.List(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if users == nil {
		users = []domain.User{}
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "users": users})
}

func (h *Handlers) CreateUser(c echo.Context) error {
	var req struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		GroupIDs []string `json:"group_ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}

	var groupIDs []uuid.UUID
	for _, id := range req.GroupIDs {
		uid, err := uuid.Parse(id)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group id: " + id})
		}
		groupIDs = append(groupIDs, uid)
	}

	user, err := h.svc.Users.Create(c.Request().Context(), app.CreateUserRequest{
		Username: req.Username,
		Password: req.Password,
		UserType: domain.UserInternal,
		GroupIDs: groupIDs,
	})
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "username already exists"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusCreated, user)
}

func (h *Handlers) UpdateUser(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}

	var req struct {
		Username string   `json:"username"`
		Password string   `json:"password"`
		GroupIDs []string `json:"group_ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}

	var groupIDs []uuid.UUID
	if req.GroupIDs != nil {
		for _, gid := range req.GroupIDs {
			uid, err := uuid.Parse(gid)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid group id"})
			}
			groupIDs = append(groupIDs, uid)
		}
	}

	if err := h.svc.Users.Update(c.Request().Context(), app.UpdateUserRequest{
		ID:       id,
		Username: req.Username,
		Password: req.Password,
		GroupIDs: groupIDs,
	}); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handlers) DeleteUser(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}
	if err := h.svc.Users.Delete(c.Request().Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) ListGroups(c echo.Context) error {
	groups, err := h.svc.Groups.List(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if groups == nil {
		groups = []domain.Group{}
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "groups": groups})
}

func (h *Handlers) CreateGroup(c echo.Context) error {
	var req struct {
		DisplayName string   `json:"display_name"`
		Permissions []string `json:"permissions"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}

	var perms []domain.Permission
	for _, p := range req.Permissions {
		perms = append(perms, domain.Permission(p))
	}

	group, err := h.svc.Groups.Create(c.Request().Context(), app.CreateGroupRequest{
		DisplayName: req.DisplayName,
		Permissions: perms,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusCreated, group)
}

func (h *Handlers) UpdateGroup(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}

	var req struct {
		DisplayName string   `json:"display_name"`
		Permissions []string `json:"permissions"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}

	var perms []domain.Permission
	if req.Permissions != nil {
		for _, p := range req.Permissions {
			perms = append(perms, domain.Permission(p))
		}
	}

	if err := h.svc.Groups.Update(c.Request().Context(), app.UpdateGroupRequest{
		ID:          id,
		DisplayName: req.DisplayName,
		Permissions: perms,
	}); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handlers) DeleteGroup(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}
	if err := h.svc.Groups.Delete(c.Request().Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) ListRegistries(c echo.Context) error {
	regs, err := h.svc.Admin.ListRegistries(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if regs == nil {
		regs = []domain.Registry{}
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "registries": regs})
}

func (h *Handlers) CreateRegistryEntry(c echo.Context) error {
	var req struct {
		URL string `json:"url"`
	}
	if err := c.Bind(&req); err != nil || req.URL == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "url required"})
	}
	r, err := h.svc.Admin.CreateRegistry(c.Request().Context(), req.URL)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusCreated, r)
}

func (h *Handlers) DeleteRegistryEntry(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid id"})
	}
	if err := h.svc.Store.DeleteRegistry(c.Request().Context(), id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) ListLogs(c echo.Context) error {
	limit := 100
	if l := c.QueryParam("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}
	logs, err := h.svc.Admin.ListLogs(c.Request().Context(), limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if logs == nil {
		logs = []domain.LogEntry{}
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "logs": logs})
}

func (h *Handlers) ListAllInstances(c echo.Context) error {
	instances, err := h.svc.Store.ListInstances(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if instances == nil {
		instances = []domain.DropletInstance{}
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "instances": instances})
}
