package selfupdate

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.1.0-dev", "0.1.0", true}, // dev → any real release
		{"1.2.0", "1.3.0", true},
		{"1.2.0", "1.2.1", true},
		{"2.0.0", "1.9.9", false},
		{"1.2.0", "1.2.0", false},
		{"v1.2.0", "1.2.0", false}, // v-prefix ignored
		{"1.2.0", "v1.3.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q,%q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	if AssetName("linux", "amd64") != "keel_linux_amd64" {
		t.Error("bad linux asset name")
	}
	if AssetName("darwin", "arm64") != "keel_darwin_arm64" {
		t.Error("bad darwin asset name")
	}
}
