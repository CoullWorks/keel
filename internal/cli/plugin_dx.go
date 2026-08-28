package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/pluginstore"
	"github.com/spf13/cobra"
)

// This file is the plugin developer experience: create a new plugin from the
// example template, test one headlessly before installing it, and publish one.
// They exist so authoring a keel plugin is a keel command rather than "copy the
// example directory by hand and hope you got the layout right", which is where
// every extension system loses its long tail of contributors.

// pluginsCreateCmd scaffolds a new runtime plugin directory from the example
// layout, ready to install with `keel plugins add`.
func pluginsCreateCmd() *cobra.Command {
	var dir string
	c := &cobra.Command{
		Use:   "create <name>",
		Args:  cobra.ExactArgs(1),
		Short: "Scaffold a new plugin from the example template",
		Long: "Writes a minimal but valid plugin directory (config/register.yaml,\n" +
			"config/event.yaml, README and LICENSE) that keel will register as soon\n" +
			"as it is installed with `keel plugins add`. Add cli/, studio/ and wizard/\n" +
			"the way the built-in example does.",
		Example: "  keel plugins create my-plugin\n" +
			"  keel plugins create my-plugin --dir ./plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := validPluginName(name); err != nil {
				return err
			}
			base := dir
			if base == "" {
				base = "."
			}
			dest := filepath.Join(base, name)
			if _, err := os.Stat(dest); err == nil {
				return fmt.Errorf("%s already exists", dest)
			}
			if err := scaffoldPlugin(dest, name); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "created %s\n", dest)
			fmt.Fprintf(out, "  edit %s\n", filepath.Join(dest, "config", "register.yaml"))
			fmt.Fprintf(out, "  test it:    keel plugins test %s\n", dest)
			fmt.Fprintf(out, "  install it: keel plugins add %s\n", dest)
			return nil
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "parent directory to create the plugin in (default: current dir)")
	return c
}

// validPluginName refuses a name that would not survive validation: the same rule
// the registry and the pluginstore apply, checked here so `create` fails fast
// rather than scaffolding a directory keel will later refuse to register.
func validPluginName(name string) error {
	if name == "" {
		return fmt.Errorf("a plugin needs a name")
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, ` /\:`) || name == "." || name == ".." {
		return fmt.Errorf("%q is not a valid plugin name: it becomes `keel %s`, so no spaces, slashes or colons", name, name)
	}
	return nil
}

// scaffoldPlugin writes the minimal valid plugin tree.
func scaffoldPlugin(dest, name string) error {
	files := map[string]string{
		filepath.Join("config", "register.yaml"): registerTemplate(name),
		filepath.Join("config", "event.yaml"):    "# Lifecycle events this plugin subscribes to. Empty means none.\nlistens: []\n",
		"README.md":                              fmt.Sprintf("# %s\n\nA keel plugin. See https://github.com/coullworks/keel for the plugin contract.\n", name),
		"LICENSE":                                "MIT\n",
	}
	for rel, content := range files {
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func registerTemplate(name string) string {
	return "# The register file. keel reads a plugin's identity from it without running\n" +
		"# any of the plugin's code. Every field below is required.\n" +
		"schema: 1\n\n" +
		"name: " + name + "\n" +
		"version: 0.1.0\n" +
		"description: A keel plugin scaffolded from the example template. Describe what it does.\n" +
		"author: you\n" +
		"license: MIT\n" +
		"homepage: https://example.com\n\n" +
		"# Narrow the plugin to particular frameworks. Omit for any framework.\n" +
		"# appliesTo: [laravel, magento]\n\n" +
		"# Powers beyond reading and writing project files, consented at trust time.\n" +
		"# capabilities: [net, secrets, exec]\n"
}

// pluginsTestCmd validates a plugin directory headlessly, before it is installed.
//
// It runs the same manifest validation the registry runs at startup (the check a
// plugin must pass to load at all) plus a smoke of its layout, so an author sees
// "this will register" or the exact reason it will not, without having to install
// it and read a studio banner.
func pluginsTestCmd() *cobra.Command {
	var strict bool
	c := &cobra.Command{
		Use:   "test <dir>",
		Args:  cobra.ExactArgs(1),
		Short: "Validate a plugin directory against the plugin standard",
		Long: "Reads the plugin's config/register.yaml and runs the same validation keel\n" +
			"runs when it loads a plugin, then checks it against the plugin standard: every\n" +
			"executable a declaration names is present and resolves inside the plugin. It\n" +
			"runs none of the plugin's code, so it is safe to point at an untrusted plugin.\n\n" +
			"Pass --strict for the release bar CI enforces: each declared executable is\n" +
			"actually executable, and the plugin ships a LICENSE and a README.",
		Example: "  keel plugins test ./my-plugin\n" +
			"  keel plugins test ./my-plugin --strict   # the bar a plugin's CI gates on",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			out := cmd.OutOrStdout()

			m, err := pluginstore.ReadMeta(dir)
			if err != nil {
				return fmt.Errorf("%s will not register: %w", dir, err)
			}
			fmt.Fprintf(out, "✓ register.yaml valid: %s %s\n", m.Name, m.Version)
			fmt.Fprintf(out, "  %s\n", m.Description)
			if len(m.Capabilities) > 0 {
				caps := make([]string, len(m.Capabilities))
				for i, c := range m.Capabilities {
					caps[i] = string(c)
				}
				fmt.Fprintf(out, "  capabilities requested: %s (consented at trust time)\n", strings.Join(caps, ", "))
			}

			// The standard check: every executable the manifest names is present (and,
			// under --strict, executable + the plugin ships LICENSE/README).
			if probs := pluginstore.Lint(dir, strict); len(probs) > 0 {
				for _, p := range probs {
					fmt.Fprintf(out, "  ✗ %s\n", p)
				}
				return fmt.Errorf("%s does not conform to the plugin standard: %d problem(s)", dir, len(probs))
			}
			if strict {
				fmt.Fprintln(out, "✓ conforms to the plugin standard (release bar)")
			} else {
				fmt.Fprintln(out, "✓ conforms to the plugin standard")
			}

			// A smoke of the layout keel picks up, reported so an author sees what will
			// load. Absence is not fatal — a data-only plugin has no bin/.
			for _, sub := range []string{"bin", "recipes", filepath.Join("config", "event.yaml")} {
				if _, err := os.Stat(filepath.Join(dir, sub)); err == nil {
					fmt.Fprintf(out, "  found %s\n", sub)
				}
			}
			fmt.Fprintf(out, "ready. install with: keel plugins add %s\n", dir)
			return nil
		},
	}
	c.Flags().BoolVar(&strict, "strict", false, "enforce the release bar: executables executable, LICENSE + README present")
	return c
}

// pluginsPublishCmd is the thin publish helper: a release checklist and, with
// --tag, a git tag. keel does not host plugins, so publishing is git, which this makes
// the last step obvious rather than performing anything keel cannot own.
func pluginsPublishCmd() *cobra.Command {
	var tag string
	c := &cobra.Command{
		Use:   "publish <dir>",
		Args:  cobra.ExactArgs(1),
		Short: "Show the release checklist for a plugin, and optionally tag it",
		Long: "keel plugins install from a git repository, so publishing a plugin is\n" +
			"pushing it and tagging a release. This prints the checklist and, with --tag,\n" +
			"creates the git tag for you.",
		Example: "  keel plugins publish ./my-plugin\n" +
			"  keel plugins publish ./my-plugin --tag v0.1.0",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			out := cmd.OutOrStdout()

			m, err := pluginstore.ReadMeta(dir)
			if err != nil {
				return fmt.Errorf("%s is not a publishable plugin: %w", dir, err)
			}
			fmt.Fprintf(out, "publishing %s %s\n\n", m.Name, m.Version)
			fmt.Fprintln(out, "checklist:")
			fmt.Fprintln(out, "  [ ] register.yaml version matches the release")
			fmt.Fprintln(out, "  [ ] README describes what it adds and any capabilities it needs")
			fmt.Fprintln(out, "  [ ] LICENSE present")
			fmt.Fprintln(out, "  [ ] pushed to a git remote users can reach")
			fmt.Fprintf(out, "\ninstall line for your README:\n  keel plugins add <owner>/%s\n", m.Name)

			if tag == "" {
				fmt.Fprintln(out, "\nre-run with --tag vX.Y.Z to create the release tag")
				return nil
			}
			// argv, never a shell string: the tag is user input.
			c := exec.CommandContext(cmd.Context(), "git", "-C", dir, "tag", tag)
			c.Stdout, c.Stderr = out, cmd.ErrOrStderr()
			if err := c.Run(); err != nil {
				return fmt.Errorf("git tag %s: %w", tag, err)
			}
			fmt.Fprintf(out, "\ntagged %s. push it with: git -C %s push origin %s\n", tag, dir, tag)
			return nil
		},
	}
	c.Flags().StringVar(&tag, "tag", "", "create this git tag (e.g. v0.1.0)")
	return c
}
