package cli

import (
	"strings"
	"testing"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/resolver"
)

func toolNames(ids []string, t *testing.T) string {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(reg, ids)
	if err != nil {
		t.Fatal(err)
	}
	var ns []string
	for _, r := range requiredTools(plan) {
		ns = append(ns, r.name)
	}
	return strings.Join(ns, ",")
}

func TestRequiredTools(t *testing.T) {
	cases := []struct {
		ids  []string
		want []string // must all be present
		not  []string // must be absent
	}{
		{[]string{"laravel", "ddev", "postgres"}, []string{"docker", "ddev", "git"}, []string{"php", "composer"}}, // containerised → no host php
		{[]string{"fastapi", "fastapi-local", "fastapi-postgres"}, []string{"uv", "git"}, []string{"docker"}},     // local python → uv, no docker
		{[]string{"nextjs", "nextjs-local"}, []string{"node", "git"}, []string{"docker"}},                         // local node
	}
	for _, c := range cases {
		got := toolNames(c.ids, t)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%v: want %q in %q", c.ids, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%v: %q should NOT be required (got %q)", c.ids, n, got)
			}
		}
	}
}
