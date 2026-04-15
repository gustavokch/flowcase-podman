package domain

import "errors"

var (
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("already exists")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
	ErrInvalidInput      = errors.New("invalid input")
	ErrInsufficientRes   = errors.New("insufficient resources")
	ErrDockerUnavailable = errors.New("docker service unavailable")
	ErrImageNotFound     = errors.New("docker image not found")
	ErrContainerFailed   = errors.New("container failed to start")
)
