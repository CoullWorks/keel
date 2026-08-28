package cli

import (
	"errors"
	"fmt"
	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/plugins"
	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/coullworks/keel/internal/tui"
	"github.com/coullworks/keel/plugin"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

// keel has three kinds of plugin.
//
// A built-in plugin is a Go package under plugins/ following the layout in
// plugins/example. It is compiled in and validated at startup, and can add
// commands, studio screens, wizard steps and event listeners.
//
// An installed plugin is the same directory layout living under the keel config
// dir, added with `keel plugins add <path|owner/repo>`. It is discovered by
// scanning, can be enabled or disabled without reinstalling, and never has its
// code run merely by being present. This is the one anybody else can use,
// because it does not require rebuilding keel.
//
// An external plugin is any executable named keel-<name> on PATH, invoked as
// `keel <name>`, git-style. It is a convenience for a tool that already exists
// as its own binary and wants a keel-shaped entry point. It cannot add a
// screen, a step or a listener, because it is not part of the build.

const pluginPrefix = plugins.Prefix

// isBuiltin reports whether name is a built-in command or alias of root.
func isBuiltin(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return true
		}
		for _, a := range c.Aliases {
			if a == name {
				return true
			}
		}
	}
	return name == "help" || name == "completion"
}

// dispatchPlugin runs keel-<name> if it exists, wiring stdio through and exposing
// KEEL_BIN so the plugin can call keel back.
//
// It reports whether it handled the call and what the process should exit with,
// rather than calling os.Exit itself: exiting from here skipped every deferred
// cleanup up the stack and made the whole path untestable.
func dispatchPlugin(errW io.Writer, name string, args []string) (handled bool, code int) {
	path, err := exec.LookPath(pluginPrefix + name)
	if err != nil {
		return false, 0
	}
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	self, _ := os.Executable()
	cmd.Env = append(os.Environ(), "KEEL_BIN="+self, "KEEL_VERSION="+Version)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return true, ee.ExitCode()
		}
		fmt.Fprintln(errW, "error:", err)
		return true, 1
	}
	return true, 0
}

func pluginsCmd() *cobra.Command {
	c := pluginsListCmd()
	// `keel plugins list` as well as bare `keel plugins`. The bare form listing
	// is the older habit, but `list` is what a person types - `keel recipes
	// list` and `keel example list` both exist - and typing it here used to
	// answer "unknown command "list" for "keel plugins"", which reads as though
	// plugins cannot be listed at all.
	list := pluginsListCmd()
	list.Use = "list"
	list.Short = "List plugins and what each one adds"
	c.AddCommand(list, pluginsAddCmd(), pluginsRemoveCmd(), pluginsEnableCmd(true), pluginsEnableCmd(false),
		pluginsTrustCmd(true), pluginsTrustCmd(false), pluginsGrantCmd(true), pluginsGrantCmd(false), pluginsDirCmd(),
		pluginsCreateCmd(), pluginsTestCmd(), pluginsPublishCmd())
	return c
}

// pluginsAddCmd installs a plugin from a folder or a repository. Both go through
// the same fetch the recipe packs use, so a local path, a GitHub shorthand and a
// full clone URL are all accepted and none of them is executed.
func pluginsAddCmd() *cobra.Command {
	var ref string
	c := &cobra.Command{
		Use:   "add <path|owner/repo|url>",
		Args:  cobra.ExactArgs(1),
		Short: "Install a plugin from a folder or a git repository",
		Example: "  keel plugins add ./my-plugin\n" +
			"  keel plugins add coullworks/keel-sonar\n" +
			"  keel plugins add https://github.com/coullworks/keel-sonar --ref v1.2.0",
		Long: "Copies the plugin into " + pluginstore.Dir() + " and enables it.\n\n" +
			"The plugin's own code is never run by installing it: keel reads\n" +
			"config/register.yaml and copies files, nothing more.",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := pluginstore.Install(cmd.Context(), args[0], ref)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "installed %s %s\n", p.Meta.Name, p.Meta.Version)
			fmt.Fprintf(out, "  %s\n", p.Meta.Description)
			fmt.Fprintf(out, "  %s\n", p.Dir)
			return nil
		},
	}
	c.Flags().StringVar(&ref, "ref", "", "git branch or tag to install")
	return c
}

func pluginsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm", "uninstall"},
		Args:    cobra.ExactArgs(1),
		Short:   "Delete an installed plugin, or disable a built-in one",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			// A built-in is compiled into this binary: there is nothing on disk to
			// delete, so "remove" means "switch it off", which is the effect the
			// user wants and which persists in plugins.yaml. Uninstalling it for
			// real would mean rebuilding keel.
			if plugins.IsBuiltin(name) {
				if err := pluginstore.SetBuiltinEnabled(name, false); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "disabled %s (a built-in cannot be uninstalled, only switched off)\n", name)
				return nil
			}
			if err := pluginstore.Remove(name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", name)
			return nil
		},
	}
}

// pluginsEnableCmd builds both `enable` and `disable`: one behaviour, one flag.
//
// It is authoritative over built-ins too. A built-in has no directory, so its off
// switch is recorded in plugins.yaml and honoured by the registry at startup;
// enabling clears that record. This is what makes `keel plugins disable sonar`
// and `enable sonar` work and persist, not just for installed plugins.
func pluginsEnableCmd(on bool) *cobra.Command {
	verb, past := "disable", "disabled"
	if on {
		verb, past = "enable", "enabled"
	}
	return &cobra.Command{
		Use:   verb + " <name>",
		Args:  cobra.ExactArgs(1),
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a plugin without removing it",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			set := pluginstore.SetEnabled
			if plugins.IsBuiltin(name) {
				set = pluginstore.SetBuiltinEnabled
			}
			if err := set(name, on); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", past, name)
			return nil
		},
	}
}

// pluginsTrustCmd builds `trust` and `untrust`.
//
// Trust is separate from install and from enable because the risks differ.
// Installing copies files and is reversible; enabling makes a plugin's declared
// screens and options appear. Neither runs anything. Trusting is the one that
// lets keel execute a plugin's own binaries, so it is its own deliberate step.
//
// Trusting also grants the capabilities the plugin's manifest declares, so trust
// is a single informed decision the same way the studio's Trust button is:
// `keel plugins test <path>` and the plugin's README name the powers up front,
// so granting them here is not silent — and each stays individually revocable
// with `keel plugins ungrant`. This is the parity fix for a plugin that was
// trusted from the terminal but then refused every capability-gated action
// because no power had been granted.
func pluginsTrustCmd(on bool) *cobra.Command {
	verb, past := "untrust", "no longer trusted"
	if on {
		verb, past = "trust", "trusted"
	}
	return &cobra.Command{
		Use:   verb + " <name>",
		Args:  cobra.ExactArgs(1),
		Short: "Allow or forbid keel running this plugin's own executables",
		Long: "Trusting a plugin lets keel run that plugin's own code. Be clear about\n" +
			"what that means: a trusted plugin's executables run with YOUR user's\n" +
			"authority, the same as installing a VS Code extension or an npm package\n" +
			"with a postinstall script. keel does not sandbox a trusted plugin. Only\n" +
			"trust a plugin whose source you would run yourself.\n\n" +
			"A plugin that only contributes data (screens, wizard options, recipes)\n" +
			"never needs this; it is required only for a plugin that runs its own\n" +
			"executables, and it is what keel checks before executing one.\n\n" +
			"Capabilities (net / secrets / exec) are a CONSENT SIGNAL, not a sandbox:\n" +
			"they record which powers you meant to allow (and keel enforces them at the\n" +
			"subprocess boundary + scrubs ambient secrets from the plugin's environment),\n" +
			"but a trusted plugin's own UI runs in the studio and is not confined by them.\n" +
			"Trusting grants the capabilities the plugin declared; revoke any one with\n" +
			"`keel plugins ungrant <name> <cap>`. Trust is bound to the plugin's\n" +
			"directory, so a same-named plugin found elsewhere must be trusted separately.",
		Example: "  keel plugins trust deploy-fly\n" +
			"  keel plugins untrust deploy-fly",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := pluginstore.SetTrusted(name, on); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s\n", name, past)
			if on {
				// Name the directory now trusted, so the user can see EXACTLY which copy
				// on disk they authorised (trust is path-bound) and that keel will run
				// its code with their authority.
				if p, ok := pluginstore.Get(name); ok && p.Dir != "" {
					fmt.Fprintf(out, "  keel will run this plugin's code (from %s) with your user's authority\n", p.Dir)
				}
				granted, err := grantDeclaredCaps(name)
				if err != nil {
					return err
				}
				if len(granted) > 0 {
					fmt.Fprintf(out, "  granted: %s (ungrant any with `keel plugins ungrant %s <cap>`)\n",
						strings.Join(granted, ", "), name)
				}
			}
			return nil
		},
	}
}

// grantDeclaredCaps grants every capability a plugin's manifest declares and
// returns the ones it granted. It mirrors the studio's Trust flow so trusting a
// plugin from either surface hands it the same powers, and a plugin that
// declares nothing grants nothing. The list is returned (not printed) so the
// caller owns the wording.
func grantDeclaredCaps(name string) ([]string, error) {
	p, ok := pluginstore.Get(name)
	if !ok {
		return nil, nil
	}
	granted := make([]string, 0, len(p.Meta.Capabilities))
	for _, c := range p.Meta.Capabilities {
		if err := pluginstore.SetCapabilityGranted(name, c, true); err != nil {
			return nil, err
		}
		granted = append(granted, string(c))
	}
	return granted, nil
}

// pluginsGrantCmd builds `grant` and `ungrant`: one capability toggle, one flag.
//
// It is the terminal counterpart to the studio's per-capability grant/ungrant
// toggles. Trust says "keel may run this plugin's code at all"; a grant says
// "and this specific power (net / secrets / exec) is allowed", and both must
// hold before a capability-gated action runs. Keeping them separate is what
// stops trust being an all-or-nothing switch — so this command exists to let a
// terminal user narrow (ungrant) or widen (grant) an individual power without
// re-trusting.
func pluginsGrantCmd(on bool) *cobra.Command {
	verb, past := "ungrant", "revoked"
	if on {
		verb, past = "grant", "granted"
	}
	return &cobra.Command{
		Use:   verb + " <name> <net|secrets|exec>",
		Args:  cobra.ExactArgs(2),
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " one capability (net / secrets / exec) for a plugin",
		Long: "Capabilities are the powers a plugin needs beyond reading and writing\n" +
			"project files:\n" +
			"  net      reach the network (a deploy integration calling a hosting API)\n" +
			"  secrets  read the project's stored secrets (an API token a deploy needs)\n" +
			"  exec     run commands on the host (git push, a vendor CLI)\n\n" +
			"A grant takes effect only once the plugin is also trusted\n" +
			"(`keel plugins trust <name>`): keel is offline by default, so both gates\n" +
			"must hold before a capability-gated action runs.",
		Example: "  keel plugins grant deploy-fly net\n" +
			"  keel plugins ungrant deploy-fly secrets",
		// Complete the capability argument so `keel plugins grant x <TAB>` offers
		// the closed set rather than nothing.
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 1 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return []string{string(plugin.CapNet), string(plugin.CapSecrets), string(plugin.CapExec)}, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, cap := args[0], plugin.Capability(args[1])
			if !plugin.KnownCapability(cap) {
				return fmt.Errorf("unknown capability %q (want: net, secrets, exec)", args[1])
			}
			if err := pluginstore.SetCapabilityGranted(name, cap, on); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s for %s\n", past, cap, name)
			// A grant is inert until the plugin is trusted; say so rather than let
			// the user wonder why the action is still refused.
			if on {
				if p, ok := pluginstore.Get(name); ok && !p.Trusted {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"note: %s is not trusted yet, so the grant is inert - run `keel plugins trust %s`\n", name, name)
				}
			}
			return nil
		},
	}
}

func pluginsDirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dir",
		Args:  cobra.NoArgs,
		Short: "Print where installed plugins live",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), pluginstore.Dir())
			return nil
		},
	}
}

func pluginsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plugins",
		Args:  cobra.NoArgs,
		Short: "List plugins and what each one adds",
		Long: "A registered plugin is a Go package under plugins/ following the layout in\n" +
			"plugins/example: config/register.yaml, and whichever of cli/, studio/ and\n" +
			"wizard/ it needs. It is validated when keel starts, so a plugin either works\n" +
			"or says why.\n\n" +
			"Any executable named keel-<name> on your PATH is also invoked as `keel <name>`,\n" +
			"git-style, and is listed separately below.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			reg := registry()

			// Problems go to stderr as well as into the table: a plugin that is
			// there but not working has to be visible on both.
			for _, err := range reg.Problems() {
				fmt.Fprintf(cmd.ErrOrStderr(), "plugin not registered: %v\n", err)
			}

			// One list, however each plugin got there: compiled into this build,
			// installed under the plugins directory, or a keel-<name> on PATH.
			// The console renders the same rows from the same builder, so the
			// two surfaces cannot disagree about what exists.
			fmt.Fprint(out, tui.RenderPlugins(plugins.Rows(reg), pluginstore.Dir()))
			return nil
		},
	}
}

// registry is loaded once: validation runs at startup, not per command.
var registry = sync.OnceValue(plugins.Load)

// reloadRegistry forces the plugin registry to rebuild on next use. keel loads it
// once per process — a real invocation runs a single command and exits, so the
// cache is exactly right there — but a test drives many commands in one process
// against different isolated plugin dirs, so it must invalidate the cache to see a
// plugin it just installed.
func reloadRegistry() { registry = sync.OnceValue(plugins.Load) }

// contributions describes what a plugin adds, so `keel plugins` answers the
// question someone actually has.
func contributions(reg *plugins.Registry, p plugin.Plugin) []string {
	var out []string
	if c, ok := p.(plugin.Commander); ok {
		for _, cmd := range c.Commands() {
			out = append(out, fmt.Sprintf("command   keel %-12s %s", cmd.Name, cmd.Summary))
		}
	}
	if s, ok := p.(plugin.Stepper); ok {
		for _, st := range s.Steps() {
			out = append(out, fmt.Sprintf("step      %-17s %s", st.ID, st.Title))
		}
	}
	if s, ok := p.(plugin.Screener); ok {
		for _, sc := range s.Screens() {
			out = append(out, fmt.Sprintf("screen    %-17s %s", sc.ID, sc.Title))
		}
	}
	if l, ok := p.(plugin.Listener); ok {
		events := make([]string, 0, len(l.Listeners()))
		for e := range l.Listeners() {
			events = append(events, string(e))
		}
		sort.Strings(events)
		for _, e := range events {
			out = append(out, fmt.Sprintf("listens   %s", e))
		}
	}
	sort.Strings(out)
	return out
}

// pluginCommands mounts every registered plugin's commands on the root, so they
// appear in help and complete like built-ins.
func pluginCommands() []*cobra.Command {
	reg := registry()
	var out []*cobra.Command
	for _, c := range reg.Commands() {
		cmd := c
		cc := &cobra.Command{
			Use:   cmd.Name,
			Short: cmd.Summary,
			// A plugin documents its own help: Summary is the one-liner, and the
			// optional Long/Example/Flags let `keel <name> --help` show the full
			// story and the flags the plugin parses itself (see below).
			Long:    cmd.Long,
			Example: cmd.Example,
			// A plugin Commander parses its own arguments (e.g. sonar's `--json`), so
			// cobra must not flag-parse (it would reject `--json` as unknown before
			// Run sees it). DisableFlagParsing passes every arg through raw; `--help`
			// is then handled explicitly below (before the project lookup, so even a
			// needs-project command shows help outside a project).
			DisableFlagParsing: true,
			RunE: func(cc *cobra.Command, args []string) error {
				for _, a := range args {
					if a == "-h" || a == "--help" {
						return cc.Help()
					}
				}
				p, err := currentProject(cmd.NeedsProject)
				if err != nil {
					return err
				}
				return cmd.Run(cc.Context(), newIO(cc.OutOrStdout(), cc.ErrOrStderr()), p, args)
			},
		}
		// Register the plugin's declared flags for documentation only. cobra does
		// not parse them (DisableFlagParsing is set — the plugin parses its own
		// args), but the help template still lists registered flags, so `keel
		// <name> --help` shows a real Flags section instead of only -h/--help.
		for _, f := range cmd.Flags {
			cc.Flags().Bool(strings.TrimPrefix(f.Name, "--"), false, f.Usage)
		}
		out = append(out, cc)
	}
	return out
}

// currentProject describes the project keel is pointed at. A command that
// declared needs_project is refused outside one, so it does not have to check.
func currentProject(required bool) (plugin.Project, error) {
	dir, err := os.Getwd()
	if err != nil {
		return plugin.Project{}, err
	}
	p := plugin.Project{Dir: dir, Name: filepath.Base(dir)}
	m, err := engine.ReadManifest(dir)
	if err != nil {
		if required {
			return p, manifestErr(err)
		}
		return p, nil
	}
	p.Framework, p.Env = m.Framework, m.Env
	return p, nil
}
