package models

import (
	"testing"
)

func TestUserGroupIDs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "g1", []string{"g1"}},
		{"multiple", "g1,g2,g3", []string{"g1", "g2", "g3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &User{Groups: tc.in}
			got := u.GroupIDs()
			if !equalStrings(got, tc.want) {
				t.Errorf("GroupIDs(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDropletRestrictedGroupIDs(t *testing.T) {
	emptyStr := ""
	listStr := "a,b"

	cases := []struct {
		name string
		in   *string
		want []string
	}{
		{"nil", nil, nil},
		{"empty string", &emptyStr, nil},
		{"two", &listStr, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Droplet{RestrictedGroups: tc.in}
			got := d.RestrictedGroupIDs()
			if !equalStrings(got, tc.want) {
				t.Errorf("RestrictedGroupIDs() = %v, want %v", got, tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
