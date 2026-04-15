package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/flowcase/flowcase/internal/domain"
	oidcpkg "github.com/flowcase/flowcase/internal/infra/oidc"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func (h *Handlers) OIDCLogin(c echo.Context) error {
	if h.oidc == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "OIDC not configured"})
	}

	state := generateState()
	c.SetCookie(&http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		MaxAge:   600,
	})

	return c.Redirect(http.StatusTemporaryRedirect, h.oidc.AuthURL(state))
}

func (h *Handlers) OIDCCallback(c echo.Context) error {
	if h.oidc == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "OIDC not configured"})
	}

	stateCookie, err := c.Cookie("oidc_state")
	if err != nil || stateCookie.Value != c.QueryParam("state") {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid state"})
	}

	code := c.QueryParam("code")
	if code == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing code"})
	}

	oidcUser, err := h.oidc.Exchange(c.Request().Context(), code)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "OIDC exchange failed"})
	}

	ctx := c.Request().Context()

	user, err := h.svc.Store.GetUserByUsername(ctx, oidcUser.Username)
	if err != nil {
		newUser := oidcUser.ToUser()
		newUser.PasswordHash = "oidc-managed"
		if err := h.svc.Store.CreateUser(ctx, newUser); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
		}

		groups, _ := h.svc.Store.ListGroups(ctx)
		for _, g := range groups {
			if g.DisplayName == "Unassigned" {
				h.svc.Store.SetUserGroups(ctx, newUser.ID, []uuid.UUID{g.ID})
				break
			}
		}
		user = newUser
	}

	tokens, err := h.svc.Auth.LoginByUser(ctx, user)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
	}

	setTokenCookies(c, tokens)

	c.SetCookie(&http.Cookie{
		Name:   "oidc_state",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	return c.Redirect(http.StatusTemporaryRedirect, "/dashboard")
}

func (h *Handlers) OIDCEnabled(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]bool{"enabled": h.oidc != nil})
}

func generateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Handlers) SetOIDC(consumer *oidcpkg.Consumer) {
	h.oidc = consumer
}

// Ensure the domain import is used
var _ = domain.UserOIDC
