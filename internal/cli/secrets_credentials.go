package cli

import (
	"fmt"
	"strings"

	"github.com/coullworks/keel/internal/creds"
	"github.com/coullworks/keel/internal/recipe"
	"github.com/coullworks/keel/internal/tui"
	"github.com/spf13/cobra"
)

func secretsCredentialsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "credentials",
		Short: "List, add and remove the credentials keel remembers for future projects",
		Long: "Private Composer repository keys and API keys you asked keel to remember,\n" +
			"so a second Magento project does not ask for the same Adobe keys again.\n\n" +
			"They live in a file of their own, readable only by you, and never in the\n" +
			"profile: a profile is ordinary settings that people copy between machines\n" +
			"and paste into issues, and credentials are not.\n\n" +
			"Values are never printed. Listing shows what is stored, not what it is.",
		Args: cobra.NoArgs,
		Example: "  keel secrets credentials            # what is remembered\n" +
			"  keel secrets credentials --add      # add one\n" +
			"  keel secrets credentials --remove repo.amasty.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			add, _ := cmd.Flags().GetBool("add")
			remove, _ := cmd.Flags().GetString("remove")

			store, err := creds.Load()
			if err != nil {
				return err
			}
			switch {
			case remove != "":
				if _, ok := store.Get(remove); !ok {
					return fmt.Errorf("nothing remembered for %q", remove)
				}
				store.Remember(creds.Value{ID: remove}) // no Remember flag = forget it
				if err := store.Save(); err != nil {
					return err
				}
				fmt.Fprintf(out, "✓ removed %s\n", remove)
				return nil
			case add:
				vals, err := askExtras(out)
				if err != nil {
					return err
				}
				if len(vals) == 0 {
					fmt.Fprintln(out, "nothing added")
					return nil
				}
				for i := range vals {
					vals[i].Remember = true
				}
				store.Remember(vals...)
				if err := store.Save(); err != nil {
					return err
				}
				fmt.Fprintf(out, "✓ remembered %d credential(s) in %s\n", len(vals), creds.Path())
				return nil
			}

			if len(store.Values) == 0 {
				fmt.Fprintf(out, "No remembered credentials. Add one with: keel secrets credentials --add\n")
				return nil
			}
			fmt.Fprintf(out, "%s\n\n", creds.Path())
			rows := make([]tui.CredentialRow, len(store.Values))
			for i, v := range store.Values {
				// Never print the secret. Show enough to recognise the entry.
				switch v.Kind {
				case recipe.CredComposer:
					who := v.Username
					if who == "" {
						who = "(token)"
					}
					rows[i] = tui.CredentialRow{Kind: "composer", ID: v.ID, Detail: who}
				default:
					rows[i] = tui.CredentialRow{Kind: "env", ID: v.ID, Detail: masked(v.Secret)}
				}
			}
			fmt.Fprintln(out, tui.RenderCredentials(rows))
			fmt.Fprintf(out, "\n%d remembered. Remove one with: keel secrets credentials --remove <id>\n", len(store.Values))
			return nil
		},
	}
	c.Flags().Bool("add", false, "add a credential interactively")
	c.Flags().String("remove", "", "forget the credential with this id")
	return c
}

// masked shows that a value exists without showing the value. Terminals get
// screenshotted and scrollback gets pasted into issues.
func masked(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return strings.Repeat("•", len(s))
	}
	return strings.Repeat("•", 8) + s[len(s)-4:]
}
