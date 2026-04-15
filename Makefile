.PHONY: all build dev clean frontend backend test

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

all: frontend backend

# Build SvelteKit static output
frontend:
	cd web && npm ci && npm run build
	rm -rf internal/embed/static/*
	cp -r web/build/* internal/embed/static/

# Build Go binary
backend:
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/flowcase ./cmd/flowcase

# Build both into a single binary
build: frontend backend

# Development: run Go server with live reload (requires air)
dev:
	FLOWCASE_DEBUG=true go run $(LDFLAGS) ./cmd/flowcase

# Development: run SvelteKit dev server
dev-frontend:
	cd web && npm run dev

# Run tests
test:
	go test ./... -v -count=1

# Clean build artifacts
clean:
	rm -rf bin/ web/build/ web/node_modules/
	rm -rf internal/embed/static/*
	touch internal/embed/static/.gitkeep

# Multi-stage Docker build
docker:
	docker build -t flowcaseweb/flowcase:$(VERSION) -f v2.Dockerfile .
