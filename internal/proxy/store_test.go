package proxy

import (
	"os"
	"testing"
)

func withStore(t *testing.T) {
	t.Helper()
	t.Setenv("KEEL_CONFIG_DIR", t.TempDir())
}

func TestRegisterAndLoad(t *testing.T) {
	withStore(t)
	if err := Register("myshop", 4100, os.Getpid(), "/tmp/myshop"); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "myshop" || got[0].Port != 4100 {
		t.Fatalf("want myshop on 4100, got %+v", got)
	}
}

// A restarted dev server gets a new port and must keep its name, not appear twice.
func TestRegisterReplacesTheSameProject(t *testing.T) {
	withStore(t)
	_ = Register("myshop", 4100, os.Getpid(), "")
	_ = Register("myshop", 4200, os.Getpid(), "")
	got, _ := Load()
	if len(got) != 1 {
		t.Fatalf("want one entry after a restart, got %d: %+v", len(got), got)
	}
	if got[0].Port != 4200 {
		t.Errorf("want the newest port, got %d", got[0].Port)
	}
}

// The case this exists for: a dev server killed with SIGKILL never deregisters,
// and routing a name at a port the kernel has since reused is worse than a 404.
func TestDeadProcessesArePruned(t *testing.T) {
	withStore(t)
	_ = Register("alive", 4100, os.Getpid(), "")
	_ = Register("gone", 4200, 0x7FFFFFFF, "") // a pid that cannot be running

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "alive" {
		t.Fatalf("want only the live project, got %+v", got)
	}

	table, err := LoadTable()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := table.Port("gone"); ok {
		t.Error("a dead project must not be routable")
	}
	if _, ok := table.Port("alive"); !ok {
		t.Error("a live project should be routable")
	}
}

func TestDeregister(t *testing.T) {
	withStore(t)
	_ = Register("myshop", 4100, os.Getpid(), "")
	if err := Deregister("myshop"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(); len(got) != 0 {
		t.Errorf("want an empty table, got %+v", got)
	}
}

func TestMissingStoreIsNotAnError(t *testing.T) {
	withStore(t)
	got, err := Load()
	if err != nil {
		t.Fatalf("a first run has no store yet, that is normal: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want nothing running, got %+v", got)
	}
}

func TestEntryURL(t *testing.T) {
	e := Entry{Name: "myshop"}
	if got := e.URL(80); got != "http://myshop.localhost/" {
		t.Errorf("on port 80 the port should not appear: %s", got)
	}
	if got := e.URL(8080); got != "http://myshop.localhost:8080/" {
		t.Errorf("off port 80 it must: %s", got)
	}
}
