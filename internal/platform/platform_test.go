package platform

import "testing"

func TestHas(t *testing.T) {
	if !Has("sh") {
		t.Fatal("expected sh on PATH")
	}
	if Has("keel-definitely-not-a-real-binary-xyz") {
		t.Fatal("did not expect a bogus binary to resolve")
	}
}

func TestInstallerCoversEnvTools(t *testing.T) {
	for _, tool := range []string{"ddev", "docker"} {
		for _, o := range []OS{
			{GOOS: "linux"},
			{GOOS: "linux", WSL: true},
			{GOOS: "darwin", HasBrew: true},
			{GOOS: "darwin"},
			{GOOS: "windows"},
		} {
			ins := Installer(tool, o)
			if ins.Guide == "" {
				t.Fatalf("Installer(%s, %+v) has no guidance", tool, o)
			}
			if ins.Auto && ins.Command == "" {
				t.Fatalf("Installer(%s, %+v) is Auto but has no Command", tool, o)
			}
		}
	}
}

func TestDetectDoesNotPanic(t *testing.T) {
	o := Detect()
	if o.GOOS == "" {
		t.Fatal("Detect returned empty GOOS")
	}
}
