package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

// searchDo issues the GitHub search request behind a package var: a test seam so
// recipesSearch's decode/render/error handling can be covered against an
// httptest server instead of the real GitHub API.
var searchDo = http.DefaultClient.Do

func recipesSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use: "search [term]",
		Long: "Finds recipe packs published on GitHub under the 'keel-recipes' topic. Nothing\n" +
			"is installed or executed: this only lists what is out there. Install one with\n" +
			"keel recipes add.\n",
		Short: "Find recipe packs on GitHub (repos tagged 'keel-recipes')",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			q := "topic:keel-recipes"
			if len(args) > 0 && args[0] != "" {
				q += " " + args[0]
			}
			u := "https://api.github.com/search/repositories?sort=stars&order=desc&q=" + url.QueryEscape(q)
			ctx, cancel := context.WithTimeout(cmd.Context(), 12*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
			req.Header.Set("Accept", "application/vnd.github+json")
			res, err := searchDo(req)
			if err != nil {
				return fmt.Errorf("searching GitHub: %w", err)
			}
			defer res.Body.Close()
			var out struct {
				Items []struct {
					FullName string `json:"full_name"`
					Desc     string `json:"description"`
					Stars    int    `json:"stargazers_count"`
				} `json:"items"`
			}
			// Cap the response: a broken or hostile endpoint should not be able to
			// stream unbounded JSON into memory.
			if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out); err != nil {
				return err
			}
			if len(out.Items) == 0 {
				fmt.Fprintln(w, "No packs found. Publish yours by adding the 'keel-recipes' GitHub topic to your repo.")
				return nil
			}
			for _, it := range out.Items {
				fmt.Fprintf(w, "★ %-5d %s\n        %s\n        keel recipes add %s\n\n", it.Stars, it.FullName, it.Desc, it.FullName)
			}
			return nil
		},
	}
}
