package cli

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/envfile"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

// envKeyPattern is the strict shell/POSIX env-var-name form: a letter or
// underscore, then letters, digits or underscores. A key outside this set would
// write a corrupt .env line (an empty name yields "=value", a space or "="
// splits the assignment, a newline injects a second line), so `secrets generate`
// rejects it before touching the file rather than silently corrupting it.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateEnvKey rejects a KEY that is not a valid env-var name.
func validateEnvKey(key string) error {
	if !envKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid env key %q: use a letter or underscore followed by letters, digits or underscores (e.g. APP_KEY)", key)
	}
	return nil
}

// secretKeys maps a framework to the env keys that must hold a generated secret.
// Kept here (not in recipes) because generation format is code, not data.
var secretKeys = map[string][]string{
	"laravel": {"APP_KEY"},
	"django":  {"DJANGO_SECRET_KEY"},
	"fastapi": {"SECRET_KEY"},
	"nextjs":  {"NEXTAUTH_SECRET"},
	"magento": {},
}

func secretsCmd() *cobra.Command {
	c := requireSubcommand(&cobra.Command{
		Use:   "secrets",
		Short: "Manage .env secrets (list, sync from .env.example, generate keys, audit)",
		Example: "  keel secrets list\n" +
			"  keel secrets sync\n" +
			"  keel secrets check\n" +
			"  keel secrets generate APP_KEY\n",
	})
	c.AddCommand(secretsListCmd(), secretsSyncCmd(), secretsCheckCmd(), secretsGenerateCmd(), secretsCredentialsCmd())
	return c
}

// secretsListCmd lists the project's resolved env vars with Next.js precedence -
// the terminal view of the studio's Env & Secrets tab. Public (NEXT_PUBLIC_) and
// ordinary config show their value; a secret shows only that it is present. A
// secret value is NEVER printed. The monorepo root fallback is applied for a
// member with no local env of its own.
func secretsListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Args:  cobra.NoArgs,
		Short: "List the project's env vars (secrets masked, public shown, with provenance)",
		Long: "Resolves .env / .env.local / .env.<NODE_ENV> with Next.js precedence and\n" +
			"prints each variable with the file it came from. A NEXT_PUBLIC_ var and\n" +
			"ordinary config show their value; a secret (password/key/token/... or a\n" +
			"credential URL) shows only that it is present - its value is never printed.\n" +
			"A monorepo member with no local env inherits the workspace root's.\n",
		Example: "  keel secrets list\n",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Must be a keel project: the surface tests require a project-scoped
			// command to refuse (not invent one) outside a project. Resolution
			// itself is manifest-free, but the manifest is the "is this a project"
			// gate the rest of the CLI uses.
			if _, err := engine.ReadManifest("."); err != nil {
				return manifestErr(err)
			}
			return runSecretsList(cmd.OutOrStdout(), ".")
		},
	}
	return c
}

// runSecretsList prints the resolved env for dir. It is separated from the cobra
// wiring so it can be tested with a writer and a temp dir.
func runSecretsList(out io.Writer, dir string) error {
	res := resolveProjectEnv(dir)
	if !res.Found {
		fmt.Fprintln(out, res.Note)
		return nil
	}
	if res.FromRoot {
		fmt.Fprintf(out, "env (inherited from the workspace root %s):\n", res.EnvDir)
	} else {
		fmt.Fprintln(out, "env:")
	}
	// One themed table, so `secrets list` reads the same as every other keel list
	// (recipes, packs) rather than hand-spaced `key = value [source]` lines.
	rows := make([][]string, 0, len(res.Vars))
	for _, v := range res.Vars {
		var shown string
		switch {
		case v.Secret && v.Present:
			shown = "••• (present)"
		case v.Secret:
			shown = "••• (empty)"
		default:
			shown = v.Value
		}
		prov := v.File
		if v.FromRoot {
			prov += ", root"
		}
		rows = append(rows, []string{v.Key, shown, prov})
	}
	if len(rows) > 0 {
		fmt.Fprintln(out, tui.RenderTable(
			[]tui.TableColumn{{Title: "Key"}, {Title: "Value"}, {Title: "Source"}},
			rows, nil))
	}
	return nil
}

// frameworkOf reads the keel manifest to learn the stack; empty if not a keel project.
func frameworkOf(dir string) string {
	if m, err := engine.ReadManifest(dir); err == nil {
		return m.Framework
	}
	return ""
}

func secretsSyncCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sync",
		Args:  cobra.NoArgs,
		Short: "Create/update .env from .env.example (never overwrites existing values) and generate missing keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			example, err := envfile.Load(".env.example")
			if err != nil {
				return err
			}
			if len(example.Pairs) == 0 {
				if _, statErr := os.Stat(".env.example"); os.IsNotExist(statErr) {
					return fmt.Errorf("no .env.example here - run inside a project that ships one")
				}
			}
			cur, err := envfile.Load(".env")
			if err != nil {
				return err
			}
			added := cur.Merge(example)

			fw := frameworkOf(".")
			var generated []string
			for _, k := range secretKeys[fw] {
				if !cur.Has(k) || strings.TrimSpace(cur.Get(k)) == "" {
					val, gerr := frameworkSecret(fw, k)
					if gerr != nil {
						return gerr
					}
					cur.Set(k, val)
					generated = append(generated, k)
				}
			}

			if err := os.WriteFile(".env", []byte(cur.Render()), 0o600); err != nil {
				return err
			}
			if err := ensureGitignored(".env"); err != nil {
				fmt.Fprintln(out, "!", err)
			}

			fmt.Fprintln(out, "✓ .env synced")
			if len(added) > 0 {
				fmt.Fprintf(out, "  added %d key(s) from .env.example: %s\n", len(added), strings.Join(added, ", "))
			}
			if len(generated) > 0 {
				fmt.Fprintf(out, "  generated: %s\n", strings.Join(generated, ", "))
			}
			if empty := cur.EmptyKeys(); len(empty) > 0 {
				fmt.Fprintf(out, "  still need values: %s\n", strings.Join(empty, ", "))
			}
			return nil
		},
	}
	return c
}

func secretsCheckCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "check",
		Args:  cobra.NoArgs,
		Short: "Audit .env: drift vs .env.example, empty/placeholder values, and whether it is committed",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cur, err := envfile.Load(".env")
			if err != nil {
				return err
			}
			example, _ := envfile.Load(".env.example")
			problems := 0

			if _, statErr := os.Stat(".env"); os.IsNotExist(statErr) {
				fmt.Fprintln(out, "✗ no .env (run: keel secrets sync)")
				// A missing .env is a failure the exit code must reflect, so
				// `keel secrets check` can gate CI the way `optimize` does.
				return fmt.Errorf("no .env")
			}
			if missing := cur.MissingFrom(example); len(missing) > 0 {
				problems++
				fmt.Fprintf(out, "✗ .env is missing keys present in .env.example: %s\n", strings.Join(missing, ", "))
			}
			if empty := cur.EmptyKeys(); len(empty) > 0 {
				problems++
				fmt.Fprintf(out, "✗ empty/placeholder values: %s\n", strings.Join(empty, ", "))
			}
			if tracked(".env") {
				problems++
				fmt.Fprintln(out, "✗ .env is tracked by git - secrets may be committed. Add it to .gitignore and run: git rm --cached .env")
			}
			if !gitignored(".env") {
				problems++
				fmt.Fprintln(out, "✗ .env is not in .gitignore")
			}
			if problems == 0 {
				fmt.Fprintln(out, "✓ secrets look healthy")
				return nil
			}
			// Exit non-zero when there are findings so `keel secrets check` gates
			// CI (consistent with `optimize`, which exits 1 on error-level issues).
			// The specific problems were already printed above; this is the summary
			// the exit code carries.
			return fmt.Errorf("%d secret issue(s) found", problems)
		},
	}
	return c
}

func secretsGenerateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "generate <KEY>",
		Example: "  keel secrets generate APP_KEY\n",
		Short:   "Generate a strong random value for KEY and write it to .env",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			key := args[0]
			if err := validateEnvKey(key); err != nil {
				return err
			}
			cur, err := envfile.Load(".env")
			if err != nil {
				return err
			}
			val, err := frameworkSecret(frameworkOf("."), key)
			if err != nil {
				return err
			}
			cur.Set(key, val)
			if err := os.WriteFile(".env", []byte(cur.Render()), 0o600); err != nil {
				return err
			}
			_ = ensureGitignored(".env")
			fmt.Fprintf(out, "✓ %s set (%d chars)\n", key, len(val))
			return nil
		},
	}
	return c
}

// frameworkSecret produces a value in the format the stack expects.
func frameworkSecret(framework, key string) (string, error) {
	if framework == "laravel" && key == "APP_KEY" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		return "base64:" + base64.StdEncoding.EncodeToString(buf), nil
	}
	return envfile.Secret(48)
}

// ensureGitignored appends a name to .gitignore if not already covered.
func ensureGitignored(name string) error {
	if gitignored(name) {
		return nil
	}
	f, err := os.OpenFile(".gitignore", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "\n# added by keel\n%s\n", name); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func gitignored(name string) bool {
	b, err := os.ReadFile(".gitignore")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == name || line == "/"+name || line == filepath.Base(name) {
			return true
		}
	}
	return false
}

// tracked reports whether git currently tracks the path.
func tracked(path string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	out, err := exec.Command("git", "ls-files", "--error-unmatch", path).CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
