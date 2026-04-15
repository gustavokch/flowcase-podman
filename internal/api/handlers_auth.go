package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/flowcase/flowcase/internal/app"
	"github.com/flowcase/flowcase/internal/domain"
	"github.com/labstack/echo/v4"
)

func (h *Handlers) Login(c echo.Context) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}

	tokens, err := h.svc.Auth.Login(c.Request().Context(), app.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	setTokenCookies(c, tokens)
	return c.JSON(http.StatusOK, map[string]string{"access_token": tokens.AccessToken})
}

func (h *Handlers) RefreshToken(c echo.Context) error {
	refreshToken := ""
	cookie, err := c.Cookie("refresh_token")
	if err == nil {
		refreshToken = cookie.Value
	}

	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.Bind(&req); err == nil && req.RefreshToken != "" {
		refreshToken = req.RefreshToken
	}

	if refreshToken == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing refresh token"})
	}

	tokens, err := h.svc.Auth.RefreshTokens(c.Request().Context(), refreshToken)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
	}

	setTokenCookies(c, tokens)
	return c.JSON(http.StatusOK, map[string]string{"access_token": tokens.AccessToken})
}

func (h *Handlers) Logout(c echo.Context) error {
	claims := GetClaims(c)
	if claims == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	_ = h.svc.Auth.Logout(c.Request().Context(), claims.UserID)
	clearTokenCookies(c)
	return c.JSON(http.StatusOK, map[string]string{"status": "logged out"})
}

func (h *Handlers) Me(c echo.Context) error {
	claims := GetClaims(c)
	if claims == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	user, err := h.svc.Users.Get(c.Request().Context(), claims.UserID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	return c.JSON(http.StatusOK, user)
}

func setTokenCookies(c echo.Context, tokens *domain.TokenPair) {
	c.SetCookie(&http.Cookie{
		Name:     "access_token",
		Value:    tokens.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   900, // 15 minutes
	})
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    tokens.RefreshToken,
		Path:     "/api/auth",
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(7 * 24 * time.Hour / time.Second),
	})
}

func clearTokenCookies(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     "access_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/api/auth",
		HttpOnly: true,
		MaxAge:   -1,
	})
}
