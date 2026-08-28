package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coullworks/keel/internal/engine"
	"github.com/coullworks/keel/internal/profile"
	"github.com/coullworks/keel/internal/project"
	"github.com/spf13/cobra"
)

func commerceCmd() *cobra.Command {
	c := requireSubcommand(&cobra.Command{
		Use:   "commerce",
		Short: "Make a store AI-agent-ready (agentic commerce): JSON-LD, product feed, agents manifest",
	})
	c.AddCommand(commerceReadyCmd())
	return c
}

func commerceReadyCmd() *cobra.Command {
	var tool string
	var force bool
	c := &cobra.Command{
		Use:   "ready [path]",
		Short: "Scaffold agentic-commerce readiness (JSON-LD + feed + agents.json), and run your readiness tool if configured",
		Long: "Makes a store discoverable and transactable by AI shopping agents. keel drops\n" +
			"a portable starter kit (structured data, a product-feed template and an\n" +
			"agents.json capability manifest) and, if you have a dedicated readiness tool,\n" +
			"runs it too. Configure the tool with --tool, KEEL_COMMERCE_TOOL, or the\n" +
			"profile's commerce_tool default. keel owns the plumbing; the tool owns the audit.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dir := "."
			if len(args) == 1 {
				dir = project.Expand(args[0])
			}
			// 1. Scaffold the portable, stack-agnostic starter kit.
			written, err := scaffoldCommerceKit(dir, force)
			if err != nil {
				return err
			}
			for _, w := range written {
				fmt.Fprintln(out, "✎", w)
			}
			if len(written) == 0 {
				fmt.Fprintln(out, "• starter kit already present (use --force to overwrite)")
			}

			// 2. Hand off to the configured readiness tool for the deeper audit.
			t := readinessTool(tool)
			if t == "" {
				fmt.Fprintln(out, "\nNext: validate structured data, then wire the feed + an MCP endpoint (see agentic-commerce/README.md).")
				fmt.Fprintln(out, "For a full readiness audit, configure a tool: keel commerce ready --tool <command>")
				return nil
			}
			fmt.Fprintf(out, "\n→ running readiness tool: %s %s\n", t, dir)
			return runReadinessTool(cmd.Context(), t, dir)
		},
	}
	c.Flags().StringVar(&tool, "tool", "", "external readiness tool to run (overrides KEEL_COMMERCE_TOOL / profile)")
	c.Flags().BoolVar(&force, "force", false, "overwrite existing starter-kit files")
	return c
}

// readinessTool resolves the external tool: flag > env > profile default.
func readinessTool(flag string) string {
	if flag != "" {
		return flag
	}
	if e := os.Getenv("KEEL_COMMERCE_TOOL"); e != "" {
		return e
	}
	if p, err := profile.Load(); err == nil {
		return p.Get("", "commerce_tool")
	}
	return ""
}

// runReadinessTool shells out to the configured tool in dir, streaming output.
func runReadinessTool(ctx context.Context, tool, dir string) error {
	fields := strings.Fields(tool)
	if len(fields) == 0 {
		// A tool configured as whitespace (`--tool "  "`, KEEL_COMMERCE_TOOL=" ")
		// passes the non-empty check at the call site but has no command to run.
		return fmt.Errorf("no readiness tool configured: set --tool, KEEL_COMMERCE_TOOL or the profile's commerce_tool to a command")
	}
	// Fail with an actionable message when the tool is not installed, rather than
	// the shell's cryptic "executable file not found in $PATH".
	if _, err := exec.LookPath(fields[0]); err != nil {
		return fmt.Errorf("readiness tool %q is not installed or not on PATH. Install it or fix --tool", fields[0])
	}
	fields = append(fields, dir)
	cmd := exec.CommandContext(ctx, fields[0], fields[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// scaffoldCommerceKit writes the portable agentic-commerce starter kit; returns
// the files it created (skips existing ones unless force).
func scaffoldCommerceKit(dir string, force bool) ([]string, error) {
	files := map[string]string{
		"agentic-commerce/README.md":      commerceReadme,
		"agentic-commerce/product.jsonld": productJSONLD,
		"agentic-commerce/agents.json":    agentsManifest,
		"agentic-commerce/feed.spec.md":   feedSpec,
	}
	var written []string
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if _, err := os.Stat(full); err == nil && !force {
			continue
		}
		if err := engine.WriteFile(dir, rel, content); err != nil {
			return written, err
		}
		written = append(written, rel)
	}
	// deterministic order for output
	for i := range written {
		for j := i + 1; j < len(written); j++ {
			if written[j] < written[i] {
				written[i], written[j] = written[j], written[i]
			}
		}
	}
	return written, nil
}

const commerceReadme = `# Agentic-commerce readiness

Make this store discoverable and transactable by AI shopping agents (ChatGPT,
Gemini, Perplexity, and the emerging Agentic Commerce Protocol / Shopify
Storefront MCP / Google UCP).

## The floor every store needs
- [ ] **Structured data**: schema.org ` + "`Product`/`Offer`" + ` JSON-LD on every product
      page (see product.jsonld). Validate at https://validator.schema.org.
- [ ] **Machine-readable product feed**: a stable, complete feed agents can crawl
      (see feed.spec.md). Google Merchant / TSV / JSON.
- [ ] **Agent access**: don't block AI crawlers in robots.txt; expose an
      agents.json capability manifest at your site root.
- [ ] **Transactable surface**: a Storefront MCP endpoint (or Agentic Commerce
      Protocol checkout) so agents can query catalog + place orders.
- [ ] **Honesty signals**: accurate price, availability, returns, shipping in
      the structured data (agents reward correctness, penalise drift).

## Wiring per stack
- **Magento / Adobe Commerce**: emit JSON-LD via a small module; use the product
  feed export; front the catalog with an MCP endpoint.
- **WooCommerce**: a plugin (or child-theme hook) prints product.jsonld; the WC
  REST API + a feed plugin cover the feed.
- **Next.js / headless**: render the JSON-LD in the product route ` + "`<script type=\"application/ld+json\">`" + `;
  serve the feed from an API route; wire a Storefront MCP server.

Run a full audit with your readiness tool: ` + "`keel commerce ready --tool <command>`" + `.
`

const productJSONLD = `{
  "@context": "https://schema.org/",
  "@type": "Product",
  "name": "{{PRODUCT_NAME}}",
  "sku": "{{SKU}}",
  "image": ["{{IMAGE_URL}}"],
  "description": "{{DESCRIPTION}}",
  "brand": { "@type": "Brand", "name": "{{BRAND}}" },
  "offers": {
    "@type": "Offer",
    "url": "{{PRODUCT_URL}}",
    "priceCurrency": "{{CURRENCY}}",
    "price": "{{PRICE}}",
    "availability": "https://schema.org/InStock",
    "itemCondition": "https://schema.org/NewCondition",
    "shippingDetails": { "@type": "OfferShippingDetails" },
    "hasMerchantReturnPolicy": { "@type": "MerchantReturnPolicy" }
  }
}
`

const agentsManifest = `{
  "$schema": "https://keel.dev/schemas/agents-commerce.json",
  "name": "{{STORE_NAME}}",
  "description": "Storefront capabilities for AI shopping agents.",
  "endpoints": {
    "product_feed": "/feed/products.json",
    "storefront_mcp": null,
    "search": "/api/search?q={query}"
  },
  "policies": {
    "returns": "/returns",
    "shipping": "/shipping",
    "privacy": "/privacy"
  },
  "capabilities": ["browse", "search", "product_details"]
}
`

const feedSpec = `# Product feed spec

Agents need a complete, stable, machine-readable feed. Serve it at a fixed URL
(referenced from agents.json) and keep it fresh.

Minimum fields per product:
- id (stable SKU), title, description, link, image_link
- price + currency, availability (in_stock | out_of_stock | preorder)
- brand, gtin/mpn where available, product_category
- shipping (price, region), return policy

Formats agents/marketplaces accept: Google Merchant XML/TSV, or JSON
(` + "`{ \"products\": [ { ... } ] }`" + `). Prefer JSON for MCP-driven agents.
`
