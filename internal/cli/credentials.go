package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/coullworks/keel/internal/creds"
	"github.com/coullworks/keel/internal/envfile"
	"github.com/coullworks/keel/internal/platform"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/resolver"
)

// askCredentials is the interactive collector behind a package var: a test seam,
// so the build path can be covered with canned answers instead of a terminal.
var askCredentials = promptCredentials

// collectCredentials gathers everything the plan needs, in this order: values
// already in the environment, values the user chose to remember, then whatever
// is still missing, asked for once.
//
// This replaced a flow that only knew about Magento and only wrote DDEV's path.
// A recipe now declares what it needs and the environment decides where it goes,
// so a private Composer repository in someone's own pack works the same way.
func collectCredentials(out io.Writer, plan *resolver.Plan, dir string, yes bool) ([]creds.Value, error) {
	want := creds.Required(plan)
	if len(want) == 0 {
		return nil, nil
	}
	store, err := creds.Load()
	if err != nil {
		return nil, err
	}

	values := make([]creds.Value, 0, len(want))
	for _, c := range want {
		v := creds.Value{ID: c.ID, Kind: c.Kind, Auth: c.Auth}
		u, s := credentialFromEnv(c)
		v.Username, v.Secret = u, s
		values = append(values, v)
	}
	values = store.Fill(values)

	// Anything still missing needs a person. --yes means nobody is there to ask,
	// so go straight to the error rather than prompting into the void.
	if !yes {
		asked, err := askCredentials(out, want, values)
		if err != nil {
			return nil, err
		}
		values = asked
	}

	// A required credential that is still missing fails here, before anything is
	// installed. Continuing only buys an authentication failure from composer
	// several minutes later, against a half-created project.
	if missing := creds.MissingRequired(plan, values); len(missing) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s cannot be installed without:", plan.Framework)
		for _, c := range missing {
			fmt.Fprintf(&b, "\n  %s (%s)", credLabel(c), c.ID)
			if c.Help != "" {
				fmt.Fprintf(&b, "\n    %s", c.Help)
			}
		}
		b.WriteString("\n\nSupply them in the environment, run without --yes to be asked, or save them with: keel secrets credentials --add")
		return nil, fmt.Errorf("%s", b.String())
	}

	store.Remember(values...)
	if err := store.Save(); err != nil {
		return nil, err
	}
	return values, nil
}

// applyCredentials writes the collected values where this environment reads
// them, and reports what it wrote. Secrets are written as files, never passed to
// a shell: a `composer config --auth` step would put the key in the build log and
// in the process list.
func applyCredentials(out io.Writer, plan *resolver.Plan, dir string, values []creds.Value) error {
	path, err := creds.WriteComposerAuth(plan.EnvFamily(), dir, values)
	if err != nil {
		return err
	}
	if path != "" {
		fmt.Fprintf(out, "✓ wrote Composer credentials to %s\n", path)
	}
	env := creds.EnvValues(values)
	if len(env) == 0 {
		return nil
	}
	path = filepath.Join(dir, ".env")
	f, err := envfile.Load(path)
	if err != nil {
		return err
	}
	for _, k := range sortedEnvKeys(env) {
		f.Set(k, env[k])
	}
	// 0600: .env now holds real API keys, not just settings.
	if err := os.WriteFile(path, []byte(f.Render()), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ wrote %d key(s) to .env\n", len(env))
	return nil
}

func sortedEnvKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// credentialFromEnv reads a credential from the process environment, which is
// how CI supplies them. MAGENTO_PUBLIC_KEY / MAGENTO_PRIVATE_KEY keep working
// because they were the documented way before this existed.
func credentialFromEnv(c recipe.Credential) (username, secret string) {
	if c.Kind == recipe.CredEnv {
		return "", os.Getenv(c.ID)
	}
	if c.ID == "repo.magento.com" {
		return os.Getenv("MAGENTO_PUBLIC_KEY"), os.Getenv("MAGENTO_PRIVATE_KEY")
	}
	// Generic: repo.example.com -> KEEL_AUTH_REPO_EXAMPLE_COM_USER / _SECRET.
	base := "KEEL_AUTH_" + envSafe(c.ID)
	return os.Getenv(base + "_USER"), os.Getenv(base + "_SECRET")
}

// envSafe turns a repository host into an environment-variable-safe name.
func envSafe(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func credLabel(c recipe.Credential) string {
	if c.Label != "" {
		return c.Label
	}
	return c.ID
}

// promptCredentials asks for each missing credential, then offers to add more:
// extra private Composer repositories and environment keys, a row at a time.
func promptCredentials(out io.Writer, want []recipe.Credential, values []creds.Value) ([]creds.Value, error) {
	byID := map[string]int{}
	for i, v := range values {
		byID[v.ID] = i
	}
	for _, c := range want {
		i, ok := byID[c.ID]
		if !ok || values[i].Filled() {
			continue
		}
		v, skipped, err := askOne(out, c)
		if err != nil {
			return nil, err
		}
		if skipped {
			continue
		}
		values[i] = v
	}
	extra, err := askExtras(out)
	if err != nil {
		return nil, err
	}
	return append(values, extra...), nil
}

// askOne asks for a single declared credential. An optional one can be skipped
// with an empty answer, because most projects need none of them.
func askOne(out io.Writer, c recipe.Credential) (creds.Value, bool, error) {
	v := creds.Value{ID: c.ID, Kind: c.Kind, Auth: c.Auth}
	fmt.Fprintf(out, "\n%s\n", credLabel(c))
	if c.Help != "" {
		fmt.Fprintf(out, "  %s\n", c.Help)
		if u := firstURL(c.Help); u != "" {
			_ = platform.OpenURL(u)
		}
	}
	if !c.Required {
		fmt.Fprintln(out, "  (optional - press Enter to skip)")
	}

	if c.Kind == recipe.CredEnv {
		if err := huh.NewInput().Title(c.ID).Value(&v.Secret).EchoMode(huh.EchoModePassword).Run(); err != nil {
			return v, false, err
		}
		return v, !v.Filled(), nil
	}
	if c.Auth == recipe.AuthBearer {
		if err := huh.NewInput().Title("Token for " + c.ID).Value(&v.Secret).EchoMode(huh.EchoModePassword).Run(); err != nil {
			return v, false, err
		}
		return v, !v.Filled(), nil
	}
	if err := huh.NewInput().Title("Username / public key for " + c.ID).Value(&v.Username).Run(); err != nil {
		return v, false, err
	}
	if strings.TrimSpace(v.Username) == "" && !c.Required {
		return v, true, nil
	}
	if err := huh.NewInput().Title("Password / private key for " + c.ID).Value(&v.Secret).EchoMode(huh.EchoModePassword).Run(); err != nil {
		return v, false, err
	}
	if !v.Filled() {
		return v, true, nil
	}
	remember := true
	if err := confirm("Remember these for future projects? (stored 0600 in "+creds.Path()+")", &remember); err != nil {
		return v, false, err
	}
	v.Remember = remember
	return v, false, nil
}

// askExtras collects rows the plan did not ask for: another private Composer
// repository, or an API key. Loops until the user says no, because a real
// Magento build often needs several extension vendors.
func askExtras(out io.Writer) ([]creds.Value, error) {
	var extra []creds.Value
	for {
		add := false
		if err := confirm("Add a credential? (private Composer repo, or an API key for .env)", &add); err != nil {
			return nil, err
		}
		if !add {
			return extra, nil
		}
		kind := recipe.CredEnv
		if err := selectOne("What kind?", []huh.Option[string]{
			huh.NewOption("Environment key (.env)", recipe.CredEnv),
			huh.NewOption("Private Composer repository (auth.json)", recipe.CredComposer),
		}, &kind); err != nil {
			return nil, err
		}
		v := creds.Value{Kind: kind}
		if kind == recipe.CredEnv {
			if err := huh.NewInput().Title("Key name").Placeholder("GOOGLE_ANALYTICS_ID").
				Suggestions(creds.CommonEnvNames).Value(&v.ID).Run(); err != nil {
				return nil, err
			}
			if err := huh.NewInput().Title("Value").EchoMode(huh.EchoModePassword).Value(&v.Secret).Run(); err != nil {
				return nil, err
			}
		} else {
			if err := huh.NewInput().Title("Repository host").Placeholder("repo.amasty.com").Value(&v.ID).Run(); err != nil {
				return nil, err
			}
			if err := huh.NewInput().Title("Username / public key").Value(&v.Username).Run(); err != nil {
				return nil, err
			}
			if err := huh.NewInput().Title("Password / private key").EchoMode(huh.EchoModePassword).Value(&v.Secret).Run(); err != nil {
				return nil, err
			}
		}
		v.ID = strings.TrimSpace(v.ID)
		if v.ID == "" || !v.Filled() {
			fmt.Fprintln(out, "  (nothing entered, skipped)")
			continue
		}
		remember := false
		if err := confirm("Remember this one for future projects?", &remember); err != nil {
			return nil, err
		}
		v.Remember = remember
		extra = append(extra, v)
	}
}

// firstURL pulls a link out of a help string so keel can open it.
func firstURL(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") {
			return strings.Trim(f, "().,")
		}
	}
	return ""
}
