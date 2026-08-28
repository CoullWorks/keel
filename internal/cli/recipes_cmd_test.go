package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecipesList(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "list")
	if err != nil {
		t.Fatalf("recipes list: %v", err)
	}
	// Built-in frameworks should appear.
	mustContain(t, out, "laravel")
}

// --packs with none installed prints the add hint.
func TestRecipesListPacksEmpty(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "list", "--packs")
	if err != nil {
		t.Fatalf("recipes list --packs: %v", err)
	}
	mustContain(t, out, "No packs installed")
}

// validate with no path lints everything installed.
func TestRecipesValidateAll(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "validate")
	if err != nil {
		t.Fatalf("recipes validate: %v", err)
	}
	mustContain(t, out, "all installed recipes valid")
}

// validate a single valid recipe file.
func TestRecipesValidateFile(t *testing.T) {
	wd := isolate(t)
	rec := "schema_version: 1\nid: demo\nkind: addon\nlabel: Demo\nappliesTo: [laravel]\n"
	p := filepath.Join(wd, "demo.yaml")
	if err := os.WriteFile(p, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runRoot(t, "recipes", "validate", p)
	if err != nil {
		t.Fatalf("validate file: %v", err)
	}
	mustContain(t, out, "valid")
}

// validate a broken recipe file errors.
func TestRecipesValidateBadFile(t *testing.T) {
	wd := isolate(t)
	p := filepath.Join(wd, "bad.yaml")
	if err := os.WriteFile(p, []byte("this: is: not: a: recipe:\n  - ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "recipes", "validate", p); err == nil {
		t.Fatal("expected a validation error")
	}
}

// validate a missing path errors.
func TestRecipesValidateMissingPath(t *testing.T) {
	isolate(t)
	if _, err := runRoot(t, "recipes", "validate", "/no/such/path.yaml"); err == nil {
		t.Fatal("expected a stat error")
	}
}

// freshness (text) reports a per-recipe table and a summary line.
func TestRecipesFreshnessText(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "freshness")
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	mustContain(t, out, "review-due", "recipes")
}

// freshness --json is machine-readable.
func TestRecipesFreshnessJSON(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "freshness", "--json")
	if err != nil {
		t.Fatalf("freshness --json: %v", err)
	}
	mustContain(t, out, `"id"`, `"status"`)
}

// freshness scoped to a framework filters the rows.
func TestRecipesFreshnessFramework(t *testing.T) {
	isolate(t)
	out, err := runRoot(t, "recipes", "freshness", "laravel", "--json")
	if err != nil {
		t.Fatalf("freshness laravel: %v", err)
	}
	mustContain(t, out, "laravel")
}

// remove of a non-installed pack errors clearly.
func TestRecipesRemoveMissing(t *testing.T) {
	isolate(t)
	_, err := runRoot(t, "recipes", "remove", "no-such-pack", "--yes")
	if err == nil {
		t.Fatal("expected error removing a missing pack")
	}
	mustContain(t, err.Error(), "not installed")
}

// The freshness classifier: unknown / fresh / review-due boundaries.
func TestFreshnessStatusBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	if s, _ := freshnessStatus("2026-08-06", now, 180); s != "fresh" {
		t.Errorf("recent = %q, want fresh", s)
	}
	if s, _ := freshnessStatus("2020-01-01", now, 180); s != "review-due" {
		t.Errorf("old = %q, want review-due", s)
	}
	if s, _ := freshnessStatus("garbage", now, 180); s != "unknown" {
		t.Errorf("garbage = %q, want unknown", s)
	}
}

// The source-trust helpers.
func TestTrustedAndRank(t *testing.T) {
	if !trusted("builtin") || !trusted("user") || trusted("pack:foo") {
		t.Error("trusted() classification wrong")
	}
	if sourceRank("builtin") != 0 || sourceRank("user") != 1 || sourceRank("pack:x") != 2 {
		t.Error("sourceRank ordering wrong")
	}
}

// `--json` is a contract: editors and scripts parse it, so the field names and
// the fact that it stays parseable both matter.
func TestRecipesListJSON(t *testing.T) {
	out, err := runRoot(t, "recipes", "list", "--json")
	if err != nil {
		t.Fatalf("recipes list --json: %v", err)
	}
	var rows []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Label   string `json:"label"`
		Source  string `json:"source"`
		Trusted bool   `json:"trusted"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not valid JSON: %v\ngot: %.200s", err, out)
	}
	if len(rows) < 10 {
		t.Fatalf("expected the whole catalogue, got %d rows", len(rows))
	}
	for _, r := range rows {
		if r.ID == "" || r.Kind == "" || r.Source == "" {
			t.Fatalf("row is missing a field the contract promises: %+v", r)
		}
	}
}
