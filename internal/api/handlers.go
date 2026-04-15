package api

import (
	"github.com/flowcase/flowcase/internal/app"
	oidcpkg "github.com/flowcase/flowcase/internal/infra/oidc"
)

type Handlers struct {
	svc  *app.Services
	oidc *oidcpkg.Consumer
}

func NewHandlers(svc *app.Services) *Handlers {
	return &Handlers{svc: svc}
}
