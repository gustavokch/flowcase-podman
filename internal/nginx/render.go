// Package nginx renders per-session nginx location blocks from the two
// pinned templates at config/nginx/. The legacy code at
// routes/droplet.py:519-545 does the same thing with str.replace +
// open().write(); we keep the same approach because the templates use
// `{name}` placeholders that conflict with Go's text/template syntax.
//
// Templates are NOT modified by this package. Callers point Renderer
// at the on-disk templates directory; the templates ship alongside
// the orchestrator binary in production.
package nginx

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Template filenames inside Renderer.TemplateDir.
const (
	ContainerTemplate = "container_template.conf"
	GuacTemplate      = "guac_template.conf"
)

// Renderer renders nginx config snippets and writes them under
// OutputDir as `<instance_id>.conf`. Construct via New or build the
// struct yourself.
type Renderer struct {
	TemplateDir string
	OutputDir   string
}

// New constructs a Renderer.
func New(templateDir, outputDir string) *Renderer {
	return &Renderer{TemplateDir: templateDir, OutputDir: outputDir}
}

// AuthHeader returns the base64-encoded `flowcase_user:<authToken>`
// string nginx forwards to the upstream container as
// `Authorization: Basic <returned>`. Mirrors droplet.py:519.
func AuthHeader(authToken string) string {
	return base64.StdEncoding.EncodeToString([]byte("flowcase_user:" + authToken))
}

// RenderContainerConfig fills the container template's four
// placeholders. {ip} isn't used here (the legacy template doesn't
// reference it), but if a future template needs it we'll pass it
// through too.
func (r *Renderer) RenderContainerConfig(instanceID, containerName, ip, authHeader string) (string, error) {
	tmpl, err := r.readTemplate(ContainerTemplate)
	if err != nil {
		return "", err
	}
	return strings.NewReplacer(
		"{instance_id}", instanceID,
		"{container_name}", containerName,
		"{ip}", ip,
		"{authHeader}", authHeader,
	).Replace(tmpl), nil
}

// RenderGuacConfig fills the guac template's two placeholders. The
// container_name and authHeader fields don't appear in the guac
// template (it proxies to the bridge by IP), but the signature
// stays parallel with RenderContainerConfig so callers can branch
// on droplet type without renaming arguments.
func (r *Renderer) RenderGuacConfig(instanceID, ip, authHeader string) (string, error) {
	tmpl, err := r.readTemplate(GuacTemplate)
	if err != nil {
		return "", err
	}
	return strings.NewReplacer(
		"{instance_id}", instanceID,
		"{ip}", ip,
		"{authHeader}", authHeader,
	).Replace(tmpl), nil
}

// WriteConfig persists `content` to <OutputDir>/<instanceID>.conf,
// creating OutputDir if needed.
func (r *Renderer) WriteConfig(instanceID, content string) error {
	if instanceID == "" {
		return errors.New("WriteConfig: instanceID required")
	}
	if err := os.MkdirAll(r.OutputDir, 0o755); err != nil {
		return fmt.Errorf("ensuring %s: %w", r.OutputDir, err)
	}
	return os.WriteFile(r.ConfigPath(instanceID), []byte(content), 0o644)
}

// RemoveConfig deletes <OutputDir>/<instanceID>.conf. Missing file is
// not an error — matches the legacy `if os.path.exists()` guard at
// droplet.py:684-685.
func (r *Renderer) RemoveConfig(instanceID string) error {
	err := os.Remove(r.ConfigPath(instanceID))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ConfigPath returns the on-disk path for instanceID's nginx config.
func (r *Renderer) ConfigPath(instanceID string) string {
	return filepath.Join(r.OutputDir, instanceID+".conf")
}

func (r *Renderer) readTemplate(name string) (string, error) {
	path := filepath.Join(r.TemplateDir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading nginx template %s: %w", path, err)
	}
	return string(b), nil
}
