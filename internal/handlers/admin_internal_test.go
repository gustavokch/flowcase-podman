package handlers

import "testing"

// TestFullImageRef covers the pure ref-rendering helper used by both
// PullImage and ImagesStatus. The same algorithm exists in
// dockerx.PullImage and droplet.spawn.go (see TODO above
// fullImageRef); these cases mirror those in
// internal/droplet/spawn_test.go::TestImageRef so the four copies
// stay in sync until the planned extraction.
func TestFullImageRef(t *testing.T) {
	cases := []struct {
		name     string
		registry *string
		image    string
		want     string
	}{
		{"nil registry", nil, "alpine:latest", "alpine:latest"},
		{"empty registry", strPtr(""), "alpine:latest", "alpine:latest"},
		{"docker.io stripped", strPtr("docker.io"), "flowcaseweb/firefox", "flowcaseweb/firefox"},
		{"docker.io subpath stripped", strPtr("docker.io/v1/"), "flowcaseweb/firefox", "flowcaseweb/firefox"},
		{"private registry prepends", strPtr("registry.example.com:5000"), "myimg:1", "registry.example.com:5000/myimg:1"},
		{"trailing slash trimmed", strPtr("registry.example.com:5000/"), "myimg:1", "registry.example.com:5000/myimg:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fullImageRef(tc.registry, tc.image)
			if got != tc.want {
				t.Errorf("fullImageRef(%v, %q) = %q, want %q", tc.registry, tc.image, got, tc.want)
			}
		})
	}
}

// TestTotalPages covers the pages-count helper that adminLogPagination
// uses. SQLAlchemy's Pagination.pages: 0 for empty, ceil(total/per_page)
// otherwise.
func TestTotalPages(t *testing.T) {
	cases := []struct {
		total, perPage, want int
	}{
		{0, 50, 0},
		{1, 50, 1},
		{50, 50, 1},
		{51, 50, 2},
		{99, 50, 2},
		{100, 50, 2},
		{101, 50, 3},
	}
	for _, tc := range cases {
		got := totalPages(tc.total, tc.perPage)
		if got != tc.want {
			t.Errorf("totalPages(%d, %d) = %d, want %d", tc.total, tc.perPage, got, tc.want)
		}
	}
}

func strPtr(s string) *string { return &s }
