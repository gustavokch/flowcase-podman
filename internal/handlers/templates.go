// Package handlers will hold the orchestrator's per-route HTTP
// handlers (T3.3+). templates.go is the part T3.2 owns: a small
// registry that parses the four pinned templates once at boot and
// renders them by name.
//
// Conversion notes from Jinja2 -> html/template:
//   {% if x %}...{% endif %}                  -> {{ if .X }}...{{ end }}
//   {% for i in xs %}...{% endfor %}          -> {{ range .Xs }}...{{ end }}
//   {{ var }}                                 -> {{ .Var }}
//   {{ 'true' if x else 'false' }}            -> {{ if .X }}true{{ else }}false{{ end }}
//   current_user.has_permission("perm_x")     -> .CurrentUser.PermX (precomputed by the handler)
//   current_user.get_groups()                 -> .CurrentUser.Groups (a []string)
//
// HTML structure is unchanged — only directives.
package handlers

import (
	"fmt"
	"html/template"
	"io"
	"path/filepath"
)

// TemplateNames lists the four templates the orchestrator owns. Used
// by the registry to error early if a file is missing.
var TemplateNames = []string{
	"404.html.tmpl",
	"login.html.tmpl",
	"dashboard.html.tmpl",
	"droplet.html.tmpl",
}

// Registry parses each template at startup and keeps them in a map
// for cheap lookup. Concurrent-safe — html/template is read-only after
// Parse.
type Registry struct {
	tmpls map[string]*template.Template
}

// NewRegistry parses every TemplateNames entry from `dir`.
func NewRegistry(dir string) (*Registry, error) {
	tmpls := make(map[string]*template.Template, len(TemplateNames))
	for _, name := range TemplateNames {
		path := filepath.Join(dir, name)
		t, err := template.New(name).ParseFiles(path)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		tmpls[name] = t
	}
	return &Registry{tmpls: tmpls}, nil
}

// Render writes the named template's output to `w`. Returns an error
// if the template isn't registered or if execution fails.
func (r *Registry) Render(w io.Writer, name string, data any) error {
	t, ok := r.tmpls[name]
	if !ok {
		return fmt.Errorf("template %q not registered", name)
	}
	return t.ExecuteTemplate(w, name, data)
}

// ---------------------------------------------------------------------
// View-model structs.
//
// Templates only see the fields below — nothing leaks the SQLAlchemy /
// SQL row shape directly. Handlers in T3.3+ build these by translating
// from internal/models.User + permissions.Check + droplet rows.
// ---------------------------------------------------------------------

// LoginData is rendered by login.html.tmpl.
type LoginData struct {
	Error string
}

// CurrentUserView mirrors the legacy `current_user.has_permission(...)`
// + `current_user.get_groups()` results, precomputed.
type CurrentUserView struct {
	ID                string
	Username          string
	Groups            []string
	PermAdminPanel    bool
	PermViewInstances bool
	PermEditInstances bool
	PermViewUsers     bool
	PermEditUsers     bool
	PermViewDroplets  bool
	PermEditDroplets  bool
	PermViewRegistry  bool
	PermEditRegistry  bool
	PermViewGroups    bool
	PermEditGroups    bool
}

// DashboardData is rendered by dashboard.html.tmpl.
type DashboardData struct {
	CurrentUser CurrentUserView
}

// DropletPageData is rendered by droplet.html.tmpl.
type DropletPageData struct {
	InstanceID string
	GuacToken  string
	Guacamole  bool
	Droplet    DropletView
}

// DropletView is the slice of droplet fields the per-instance page
// renders.
type DropletView struct {
	ID          string
	DisplayName string
	DropletType string
	ImagePath   string
}
