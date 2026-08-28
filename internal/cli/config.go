package cli

// config.go is the terminal counterpart to the studio's Settings page: it views
// and edits the engineer profile (internal/profile) non-interactively, using the
// SAME store `keel init` and the studio's /api/profile both read and write, so
// the three can never disagree. `keel init` is the guided wizard; `keel config`
// is the one-setting-at-a-time complement for scripts and quick edits.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/coullworks/keel/internal/catalog"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/spf13/cobra"
)

// configKeys is the closed set of profile settings `keel config get/set` exposes,
// each mapped to its accessor on a Profile. A closed set is the only safe form
// for a writer: an unknown key is refused rather than silently creating a
// setting nothing reads. name identity, projects_dir and the default stack layers
// live in Defaults; git identity lives in its own block.
var configKeys = map[string]struct {
	get func(p *profile.Profile) string
	set func(p *profile.Profile, v string)
}{
	"name":         {func(p *profile.Profile) string { return p.Git.Name }, func(p *profile.Profile, v string) { p.Git.Name = v }},
	"email":        {func(p *profile.Profile) string { return p.Git.Email }, func(p *profile.Profile, v string) { p.Git.Email = v }},
	"projects_dir": defaultKey("projects_dir"),
	"framework":    defaultKey("framework"),
	"env":          defaultKey("env"),
	"database":     defaultKey("database"),
	"frontend":     defaultKey("frontend"),
	"webserver":    defaultKey("webserver"),
	"hosting":      defaultKey("hosting"),
}

// defaultKey wires a config key to a Defaults map entry (the common case).
func defaultKey(k string) struct {
	get func(p *profile.Profile) string
	set func(p *profile.Profile, v string)
} {
	return struct {
		get func(p *profile.Profile) string
		set func(p *profile.Profile, v string)
	}{
		get: func(p *profile.Profile) string { return p.Defaults[k] },
		set: func(p *profile.Profile, v string) { p.Defaults[k] = v },
	}
}

// configKeyNames returns the settable/gettable keys, sorted, for help text and
// error messages.
func configKeyNames() []string {
	names := make([]string, 0, len(configKeys))
	for k := range configKeys {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func configCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "View or set your engineer profile defaults (the same defaults keel init writes)",
		Long: "Reads and writes the profile keel pre-fills every new project from - the same\n" +
			"file the studio's Settings page and keel init edit. `keel config` prints the\n" +
			"whole profile; get/set read or write one setting non-interactively, for\n" +
			"scripts and quick edits.\n\n" +
			"Keys: " + strings.Join(configKeyNames(), ", "),
		Example: "  keel config                      # print the current profile\n" +
			"  keel config get hosting\n" +
			"  keel config set hosting fly\n" +
			"  keel config set projects_dir ~/code\n",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runConfigList(cmd.OutOrStdout()) },
	}
	c.AddCommand(configListCmd(), configGetCmd(), configSetCmd())
	return c
}

func configListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Print the current profile",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runConfigList(cmd.OutOrStdout()) },
	}
}

func configGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "get <key>",
		Short:     "Print one profile setting",
		Example:   "  keel config get hosting\n",
		Args:      cobra.ExactArgs(1),
		ValidArgs: configKeyNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigGet(cmd.OutOrStdout(), args[0])
		},
	}
}

func configSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "set <key> <value>",
		Short:     "Set one profile setting and save",
		Example:   "  keel config set hosting fly\n",
		Args:      cobra.ExactArgs(2),
		ValidArgs: configKeyNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd.OutOrStdout(), args[0], args[1])
		},
	}
}

// runConfigList prints the whole profile as aligned key = value lines, in a
// stable order. projects_dir shows "(current directory)" for the empty default,
// which is what the empty string means (see profile.Default).
func runConfigList(out io.Writer) error {
	p, err := profile.Load()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "profile:", profile.Path())
	for _, k := range configKeyNames() {
		v := configKeys[k].get(p)
		if k == "projects_dir" && v == "" {
			v = "(current directory)"
		}
		fmt.Fprintf(out, "  %-13s %s\n", k, v)
	}
	return nil
}

// runConfigGet prints one setting's value (empty line for an unset value).
func runConfigGet(out io.Writer, key string) error {
	entry, ok := configKeys[key]
	if !ok {
		return unknownConfigKey(key)
	}
	p, err := profile.Load()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, entry.get(p))
	return nil
}

// runConfigSet validates then writes one setting through the profile store. The
// value is validated where a closed set exists (hosting against the known deploy
// targets); free-form settings (name, projects_dir) take the value verbatim.
func runConfigSet(out io.Writer, key, value string) error {
	entry, ok := configKeys[key]
	if !ok {
		return unknownConfigKey(key)
	}
	if err := validateConfigValue(key, value); err != nil {
		return err
	}
	p, err := profile.Load()
	if err != nil {
		return err
	}
	entry.set(p, value)
	if err := p.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "set %s = %s\n", key, value)
	return nil
}

// validateConfigValue rejects a value a setting cannot hold. hosting is checked
// against the profile's known deploy targets (the same list onboarding offers),
// and framework against the recipe registry's framework ids — both because a
// value nothing maps to would silently make a later command (`keel deploy`, `keel
// new`) do the wrong thing, and `keel new <framework>` already rejects an unknown
// framework, so `keel config set framework` must not accept one it forbids.
// Neither check invents a second source of truth: hosting reads
// profile.HostingOptions, framework reads the same registry `keel new` does. An
// empty value clears the setting (opting out), which is always allowed. Other
// keys are free-form.
func validateConfigValue(key, value string) error {
	switch key {
	case "hosting":
		for _, h := range profile.HostingOptions {
			if h.Key == value {
				return nil
			}
		}
		return fmt.Errorf("hosting must be one of: %s", hostingKeys())
	case "framework":
		if value == "" {
			return nil // clearing the default is allowed
		}
		reg, err := catalog.Registry()
		if err != nil {
			return err
		}
		if r, ok := reg.Get(value); !ok || r.Kind != recipe.Framework {
			return fmt.Errorf("unknown framework %q (see docs/ROUTES.md)", value)
		}
	}
	return nil
}

// hostingKeys lists the valid hosting targets for an error message.
func hostingKeys() string {
	keys := make([]string, 0, len(profile.HostingOptions))
	for _, h := range profile.HostingOptions {
		keys = append(keys, h.Key)
	}
	return strings.Join(keys, ", ")
}

// unknownConfigKey is the single wording for a key that is not in the closed set.
func unknownConfigKey(key string) error {
	return fmt.Errorf("unknown config key %q (valid keys: %s)", key, strings.Join(configKeyNames(), ", "))
}
