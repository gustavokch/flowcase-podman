package api

import (
	"io/fs"
	"net/http"

	"github.com/flowcase/flowcase/internal/app"
	embedfs "github.com/flowcase/flowcase/internal/embed"
	"github.com/flowcase/flowcase/internal/infra/config"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func NewRouter(svc *app.Services, cfg *config.Config) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete},
		AllowHeaders:     []string{echo.HeaderContentType, echo.HeaderAuthorization},
		AllowCredentials: true,
	}))

	if cfg.Debug {
		e.Use(middleware.Logger())
	}

	h := NewHandlers(svc)
	jwtMiddleware := JWTMiddleware(svc.Auth)

	api := e.Group("/api")

	auth := api.Group("/auth")
	auth.POST("/login", h.Login)
	auth.POST("/refresh", h.RefreshToken)
	auth.POST("/logout", h.Logout, jwtMiddleware)
	auth.GET("/me", h.Me, jwtMiddleware)
	auth.GET("/oidc/enabled", h.OIDCEnabled)
	auth.GET("/oidc/login", h.OIDCLogin)
	auth.GET("/oidc/callback", h.OIDCCallback)

	droplets := api.Group("/droplets", jwtMiddleware)
	droplets.GET("", h.ListDroplets)
	droplets.POST("", h.CreateDroplet, RequirePermission(svc, "edit_droplets"))
	droplets.GET("/:id", h.GetDroplet)
	droplets.PUT("/:id", h.UpdateDroplet, RequirePermission(svc, "edit_droplets"))
	droplets.DELETE("/:id", h.DeleteDroplet, RequirePermission(svc, "edit_droplets"))

	instances := api.Group("/instances", jwtMiddleware)
	instances.GET("", h.ListInstances)
	instances.POST("", h.CreateInstance)
	instances.DELETE("/:id", h.DestroyInstance)

	admin := api.Group("/admin", jwtMiddleware, RequirePermission(svc, "admin_panel"))
	admin.GET("/system", h.GetSystemInfo)
	admin.GET("/users", h.ListUsers)
	admin.POST("/users", h.CreateUser)
	admin.PUT("/users/:id", h.UpdateUser)
	admin.DELETE("/users/:id", h.DeleteUser)
	admin.GET("/groups", h.ListGroups)
	admin.POST("/groups", h.CreateGroup)
	admin.PUT("/groups/:id", h.UpdateGroup)
	admin.DELETE("/groups/:id", h.DeleteGroup)
	admin.GET("/registries", h.ListRegistries)
	admin.POST("/registries", h.CreateRegistryEntry)
	admin.DELETE("/registries/:id", h.DeleteRegistryEntry)
	admin.GET("/logs", h.ListLogs)
	admin.GET("/instances", h.ListAllInstances)
	admin.GET("/networks", h.ListNetworks)
	admin.GET("/images/status", h.ListImageStatus)
	admin.POST("/images/pull", h.PullImage)

	api.GET("/events", h.SSE, jwtMiddleware)

	e.GET("/desktop/:instanceId/*", h.ProxyDesktop, jwtMiddleware)

	staticFS, err := fs.Sub(embedfs.StaticFS, "static")
	if err == nil {
		fileServer := http.FileServer(http.FS(staticFS))
		e.GET("/*", echo.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to serve the file; if not found, serve index.html for SPA routing
			f, err := staticFS.Open(r.URL.Path[1:])
			if err != nil {
				r.URL.Path = "/"
			} else {
				f.Close()
			}
			fileServer.ServeHTTP(w, r)
		})))
	}

	return e
}
