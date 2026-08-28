package pack

import "testing"

func TestSatisfiesKeel(t *testing.T) {
	cases := []struct {
		c, v string
		want bool
	}{
		{"", "0.1.0", true},
		{">= 0.1.0", "0.1.0", true},
		{">= 0.1.0", "0.2.0", true},
		{">= 0.4.0", "0.1.0-dev", false},
		{">= 0.4.0", "0.4.0", true},
		{">= 1.0.0", "0.9.9", false},
	}
	for _, tc := range cases {
		got, err := SatisfiesKeel(tc.c, tc.v)
		if err != nil {
			t.Fatalf("SatisfiesKeel(%q,%q): %v", tc.c, tc.v, err)
		}
		if got != tc.want {
			t.Errorf("SatisfiesKeel(%q,%q)=%v want %v", tc.c, tc.v, got, tc.want)
		}
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
	r := &Registry{}
	r.Upsert(Installed{Name: "acme", Version: "1.0.0", Commit: "abc"})
	r.Upsert(Installed{Name: "acme", Version: "1.1.0", Trusted: true}) // replaces by name
	if len(r.Packs) != 1 {
		t.Fatalf("upsert should replace, got %d entries", len(r.Packs))
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := got.Get("acme")
	if !ok || p.Version != "1.1.0" || !p.Trusted {
		t.Fatalf("round-trip wrong: %+v", got.Packs)
	}
	if !got.Remove("acme") || len(got.Packs) != 0 {
		t.Fatal("remove failed")
	}
}
