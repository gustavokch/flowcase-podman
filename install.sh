#!/bin/bash

# Flowcase Installation Script
# This script automates the setup of Flowcase
#
# Local deployment notes:
#   - Traefik + Authentik (HTTPS / Let's Encrypt) are bypassed.
#   - nginx is published on the Tailscale interface IP, port 5544, only.

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Functions
print_header() {
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}========================================${NC}"
}

print_success() { echo -e "${GREEN}✓ $1${NC}"; }
print_error()   { echo -e "${RED}✗ $1${NC}"; }
print_warning() { echo -e "${YELLOW}⚠ $1${NC}"; }
print_info()    { echo -e "${BLUE}ℹ $1${NC}"; }

# Legacy containers from the pre-bypass stack (Traefik/Authentik) that are no
# longer managed by docker-compose.yml and must be cleaned up explicitly.
LEGACY_CONTAINERS="flowcase-traefik-1 authentik_server flowcase-worker-1 flowcase-postgresql-1 flowcase-redis-1"

# Rootful Podman's API socket (/run/podman/podman.sock) is root-owned, so the
# engine and compose calls need root. SUDO is filled in after engine detection
# (Podman + non-root => "sudo"); the interactive prompts and generated .env stay
# owned by the calling user. Empty when already root or using rootless Docker.
SUDO=""

print_header "Flowcase Installation Script"

# Check prerequisites
print_info "Checking prerequisites..."

# Detect container engine (Podman preferred) and its compose command
if command -v podman &> /dev/null; then
    # Rootful socket needs root; auto-elevate engine + compose calls when not root.
    [ "$EUID" -ne 0 ] && SUDO="sudo"
    CLI="$SUDO podman"
    if podman compose version &> /dev/null; then
        COMPOSE="$SUDO podman compose"
    elif command -v podman-compose &> /dev/null; then
        COMPOSE="$SUDO podman-compose"
    else
        print_error "Podman found but neither 'podman compose' nor 'podman-compose' is available."
        exit 1
    fi
    print_success "Podman is installed ($COMPOSE)"
    # Flowcase talks to the rootful Docker-compatible API socket (root-owned, so
    # test it with $SUDO — an unprivileged 'test -S' gives a false negative).
    if ! $SUDO test -S /run/podman/podman.sock; then
        print_warning "/run/podman/podman.sock not found. Enable it with: sudo systemctl enable --now podman.socket"
    fi
elif command -v docker &> /dev/null; then
    CLI=docker
    if ! docker compose version &> /dev/null; then
        print_error "Docker Compose is not installed or not working."
        exit 1
    fi
    COMPOSE="docker compose"
    print_warning "Using Docker. Flowcase now targets Podman; Docker Hub pull-rate limits may apply."
else
    print_error "No container engine found. Install Podman (recommended) or Docker first."
    echo "Podman: https://podman.io/docs/installation"
    exit 1
fi

# Check the engine is reachable
if ! $CLI info &> /dev/null; then
    print_error "$CLI is not responding. Is the service running?"
    exit 1
fi
print_success "$CLI is running"

# Check Tailscale (required for the bind IP)
if ! command -v tailscale &> /dev/null; then
    print_warning "Tailscale CLI not found; you will need to enter the bind IP manually."
    DETECTED_TS_IP=""
else
    DETECTED_TS_IP=$(tailscale ip -4 2>/dev/null | head -1 || true)
    if [ -n "$DETECTED_TS_IP" ]; then
        print_success "Tailscale IP detected: $DETECTED_TS_IP"
    else
        print_warning "Tailscale installed but no IPv4 address found (is it up?)."
    fi
fi

# Optional: tear down + recreate the existing stack first
print_header "Existing Stack"
if [ -n "$($COMPOSE ps -q 2>/dev/null)" ] || $CLI ps -a --format '{{.Names}}' | grep -qE "flowcase|authentik"; then
    print_warning "An existing Flowcase stack (or legacy Traefik/Authentik containers) was detected."
    read -p "Tear it down and recreate from scratch? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        read -p "Also delete data volumes? THIS DESTROYS ALL DATA (y/N) " -n 1 -r
        echo
        DOWN_FLAGS="--remove-orphans"
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            DOWN_FLAGS="--remove-orphans --volumes"
            print_warning "Data volumes will be removed."
        else
            print_info "Data volumes will be preserved."
        fi
        print_info "Stopping current compose project..."
        $COMPOSE down $DOWN_FLAGS || true
        # Remove any leftover legacy containers not owned by this compose file.
        for c in $LEGACY_CONTAINERS; do
            if $CLI ps -a --format '{{.Names}}' | grep -qx "$c"; then
                print_info "Removing legacy container: $c"
                $CLI rm -f "$c" || true
            fi
        done
        print_success "Teardown complete"
    else
        print_info "Leaving existing stack in place (services will be recreated as needed)."
    fi
else
    print_info "No existing stack detected."
fi

# Generate random passwords
generate_password()   { openssl rand -base64 24 | tr -d "=+/" | cut -c1-24; }
generate_secret_key() { openssl rand -base64 32 | tr -d "=+/" | cut -c1-50; }

# Configuration
print_header "Configuration"

# Tailscale bind IP
read -p "Enter the Tailscale bind IP (default: ${DETECTED_TS_IP:-none}): " TS_IP
TS_IP=${TS_IP:-$DETECTED_TS_IP}
if [ -z "$TS_IP" ]; then
    print_error "No bind IP provided and none could be detected. Aborting."
    exit 1
fi
print_info "nginx will listen on ${TS_IP}:5544 (HTTP only, HTTPS/Let's Encrypt bypassed)"

# Domain is kept for compatibility only (HTTPS path is bypassed)
read -p "Enter your domain (default: localhost): " DOMAIN
DOMAIN=${DOMAIN:-localhost}

# Check if .env already exists
if [ -f .env ]; then
    print_warning ".env file already exists"
    read -p "Do you want to overwrite it? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        print_info "Keeping existing .env file"
        SKIP_ENV=true
    else
        print_info "Backing up existing .env to .env.backup"
        cp .env .env.backup
    fi
fi

# Generate passwords (retained so Authentik can be re-enabled later)
print_info "Generating secure passwords..."
PG_PASS=$(generate_password)
AUTHENTIK_SECRET_KEY=$(generate_secret_key)
# HTTPS bypassed: staging CA kept as a harmless placeholder.
CA_SERVER="https://acme-staging-v02.api.letsencrypt.org/directory"
ADMIN_EMAIL="admin@example.com"

# Create .env file
if [ "$SKIP_ENV" != "true" ]; then
    print_info "Creating .env file..."
    cat > .env << EOF
# Flowcase Environment Variables
# Generated by install.sh on $(date)

# Domain Configuration
DOMAIN=$DOMAIN

# Tailscale interface bind IP (nginx published here only, port 5544)
TS_IP=$TS_IP

# Traefik Configuration (HTTPS/Let's Encrypt bypassed in docker-compose.yml)
ADMIN_EMAIL=$ADMIN_EMAIL
CA_SERVER=$CA_SERVER

# PostgreSQL Configuration
PG_PASS=$PG_PASS

# Authentik Configuration
AUTHENTIK_SECRET_KEY=$AUTHENTIK_SECRET_KEY

# Container engine: rootful Podman's Docker-compatible API socket
DOCKER_HOST=unix:///run/podman/podman.sock

# Pull Flowcase's own images (flowcase-guac, etc.) from an alternative registry
# to avoid Docker Hub rate limits. Leave unset to use Docker Hub (flowcaseweb).
FLOWCASE_IMAGE_REGISTRY=quay.io/gustavokch

# Registry credentials (only needed for PRIVATE image repos; public Quay repos
# need none). For Docker Hub, create a token at https://hub.docker.com/settings/security
# FLOWCASE_DOCKER_USERNAME=your-registry-username
# FLOWCASE_DOCKER_PASSWORD=your-registry-token
# FLOWCASE_DOCKER_REGISTRY=https://index.docker.io/v1/
EOF
    print_success ".env file created"
else
    # Ensure TS_IP exists even when keeping an old .env
    if ! grep -q "^TS_IP=" .env; then
        printf "\n# Tailscale interface bind IP (nginx published here only, port 5544)\nTS_IP=%s\n" "$TS_IP" >> .env
        print_info "Added TS_IP to existing .env"
    fi

    # Ensure the Podman socket + image-registry settings exist on an old .env
    if ! grep -q "^DOCKER_HOST=" .env; then
        printf "\n# Container engine: rootful Podman's Docker-compatible API socket\nDOCKER_HOST=unix:///run/podman/podman.sock\n" >> .env
        print_info "Added DOCKER_HOST to existing .env"
    fi
    if ! grep -q "FLOWCASE_IMAGE_REGISTRY" .env; then
        printf "\n# Pull Flowcase images from an alternative registry to avoid Docker Hub limits\nFLOWCASE_IMAGE_REGISTRY=quay.io/gustavokch\n" >> .env
        print_info "Added FLOWCASE_IMAGE_REGISTRY to existing .env"
    fi
fi

# Start services
print_header "Starting Flowcase"
print_info "This may take a few minutes on first run..."

$COMPOSE up -d

print_success "Containers started"

# Wait for services to be ready
print_info "Waiting for services to be ready..."
sleep 10

# Check service status
print_header "Service Status"
$COMPOSE ps

# Display access information
print_header "Installation Complete!"
echo ""
print_success "Flowcase is now running!"
echo ""
echo "Access Information:"
echo "  - Flowcase: http://${TS_IP}:5544"
echo ""
print_info "Default admin credentials will be displayed in the logs."
echo "View logs with: $COMPOSE logs -f"
echo ""
print_warning "Look for the default admin username and password in the logs!"
echo ""
print_info "To view logs: $COMPOSE logs -f"
print_info "To stop: $COMPOSE down"
print_info "To restart: $COMPOSE restart"
echo ""
print_info "For detailed setup instructions, see SETUP.md"
echo ""

# Show logs for a few seconds to catch credentials
print_info "Showing recent logs (look for default credentials)..."
echo ""
$COMPOSE logs --tail=50 web | grep -A 10 "Created default users" || true
echo ""

print_success "Installation complete! 🎉"
