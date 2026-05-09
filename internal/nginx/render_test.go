package nginx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureDir copies the pinned templates from config/nginx/ into a
// fresh temp dir so tests can render against them without depending
// on the test's cwd.
func fixtureDir(t *testing.T) (templateDir, outputDir string) {
	t.Helper()
	root := t.TempDir()
	templateDir = filepath.Join(root, "templates")
	outputDir = filepath.Join(root, "out")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Pull the real pinned files so any drift is caught here too.
	for _, name := range []string{ContainerTemplate, GuacTemplate} {
		src := filepath.Join("..", "..", "config", "nginx", name)
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(templateDir, name), b, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return templateDir, outputDir
}

func TestAuthHeaderMatchesPython(t *testing.T) {
	// base64("flowcase_user:abc") == "Zmxvd2Nhc2VfdXNlcjphYmM="
	got := AuthHeader("abc")
	if got != "Zmxvd2Nhc2VfdXNlcjphYmM=" {
		t.Errorf("AuthHeader(abc) = %q, want Zmxvd2Nhc2VfdXNlcjphYmM=", got)
	}
}

func TestRenderContainerConfigReplacesAllPlaceholders(t *testing.T) {
	tdir, odir := fixtureDir(t)
	r := New(tdir, odir)

	out, err := r.RenderContainerConfig(
		"INST123",
		"flowcase_generated_INST123",
		"10.0.0.5",
		AuthHeader("the-auth-token"),
	)
	if err != nil {
		t.Fatalf("RenderContainerConfig: %v", err)
	}

	for _, placeholder := range []string{"{instance_id}", "{container_name}", "{ip}", "{authHeader}"} {
		if strings.Contains(out, placeholder) {
			t.Errorf("rendered config still contains %s", placeholder)
		}
	}
	for _, expected := range []string{
		"location /desktop/INST123/vnc/",
		"location /desktop/INST123/audio/",
		"location /desktop/INST123/uploads/",
		"proxy_pass https://flowcase_generated_INST123:6901/",
		"Authorization \"Basic " + AuthHeader("the-auth-token") + "\"",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("rendered config missing %q", expected)
		}
	}
}

func TestRenderGuacConfigReplacesPlaceholders(t *testing.T) {
	tdir, odir := fixtureDir(t)
	r := New(tdir, odir)

	out, err := r.RenderGuacConfig("INSTGUAC", "172.20.0.7", "ignored")
	if err != nil {
		t.Fatalf("RenderGuacConfig: %v", err)
	}
	for _, placeholder := range []string{"{instance_id}", "{ip}"} {
		if strings.Contains(out, placeholder) {
			t.Errorf("rendered guac config still contains %s", placeholder)
		}
	}
	for _, expected := range []string{
		"location /desktop/INSTGUAC/vnc/",
		"proxy_pass http://172.20.0.7:8080/",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("rendered guac config missing %q", expected)
		}
	}
}

func TestWriteConfigPersistsAndIsReadable(t *testing.T) {
	tdir, odir := fixtureDir(t)
	r := New(tdir, odir)

	const content = "rendered nginx body"
	if err := r.WriteConfig("INSTW", content); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	got, err := os.ReadFile(r.ConfigPath("INSTW"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != content {
		t.Errorf("file content = %q, want %q", string(got), content)
	}
}

func TestWriteConfigCreatesOutputDir(t *testing.T) {
	tdir := t.TempDir()
	odir := filepath.Join(t.TempDir(), "deeply", "nested", "out")
	r := New(tdir, odir)

	if err := r.WriteConfig("X", "y"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if _, err := os.Stat(odir); err != nil {
		t.Errorf("output dir not created: %v", err)
	}
}

func TestWriteConfigEmptyInstanceID(t *testing.T) {
	r := New("", t.TempDir())
	if err := r.WriteConfig("", "anything"); err == nil {
		t.Error("expected error on empty instanceID")
	}
}

func TestRemoveConfigSilentOnMissing(t *testing.T) {
	r := New("", t.TempDir())
	if err := r.RemoveConfig("never-written"); err != nil {
		t.Errorf("RemoveConfig of missing file should be silent, got %v", err)
	}
}

func TestRemoveConfigDeletesExisting(t *testing.T) {
	tdir, odir := fixtureDir(t)
	r := New(tdir, odir)

	if err := r.WriteConfig("INSTX", "body"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if err := r.RemoveConfig("INSTX"); err != nil {
		t.Fatalf("RemoveConfig: %v", err)
	}
	if _, err := os.Stat(r.ConfigPath("INSTX")); !os.IsNotExist(err) {
		t.Errorf("file still exists after RemoveConfig: err=%v", err)
	}
}

func TestReadTemplateMissingFails(t *testing.T) {
	r := New(t.TempDir(), t.TempDir())
	if _, err := r.RenderContainerConfig("a", "b", "c", "d"); err == nil {
		t.Error("expected error reading missing template")
	}
}
