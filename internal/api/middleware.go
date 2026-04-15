package api

import (
	"net/http"
	"strings"

	"github.com/flowcase/flowcase/internal/app"
	"github.com/flowcase/flowcase/internal/domain"
	"github.com/labstack/echo/v4"
)

const contextKeyUser = "user_claims"

func JWTMiddleware(auth *app.AuthService) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tokenStr := ""

			// Check Authorization header first
			if authHeader := c.Request().Header.Get("Authorization"); authHeader != "" {
				if strings.HasPrefix(authHeader, "Bearer ") {
					tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
				}
			}

			// Fall back to cookie
			if tokenStr == "" {
				cookie, err := c.Cookie("access_token")
				if err == nil {
					tokenStr = cookie.Value
				}
			}

			if tokenStr == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
			}

			claims, err := auth.ValidateAccessToken(tokenStr)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			}

			c.Set(contextKeyUser, claims)
			return next(c)
		}
	}
}

func GetClaims(c echo.Context) *domain.Claims {
	claims, ok := c.Get(contextKeyUser).(*domain.Claims)
	if !ok {
		return nil
	}
	return claims
}

func RequirePermission(svc *app.Services, permission string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			claims := GetClaims(c)
			if claims == nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			}

			for _, p := range claims.Permissions {
				if string(p) == permission {
					return next(c)
				}
			}

			return c.JSON(http.StatusForbidden, map[string]string{"error": "insufficient permissions"})
		}
	}
}
