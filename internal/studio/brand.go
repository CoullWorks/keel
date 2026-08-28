package studio

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/coullworks/keel/internal/brand"
	"github.com/coullworks/keel/internal/engine"
)

// This file owns the studio's brand surface: the GLOBAL default editor
// (Settings → Brand, over ~/.config/keel/brand.yaml) and the PER-PROJECT
// override (the project view's Brand tab, over the manifest's brand block).
// Both are thin transports over the brand package — the studio never derives a
// scale or maps a token itself; it serialises what brand.Generate / Detect /
// Resolve produce so the CLI, wizard and studio can never drift. The legacy
// two-colour handleBrand (below) is kept as a back-compat shim.

// --- DTOs -------------------------------------------------------------------

// scaleDTO is one colour role's 50-950 ramp as an ordered list of (step, hex)
// so the UI renders swatches in canonical order without knowing the stop set
// (a JSON object would lose order and force the client to re-sort).
type scaleDTO struct {
	Step int    `json:"step"`
	Hex  string `json:"hex"`
}

// rolesDTO is the full semantic palette, each role an ordered ramp.
type rolesDTO struct {
	Brand       []scaleDTO `json:"brand"`
	Accent      []scaleDTO `json:"accent"`
	Neutral     []scaleDTO `json:"neutral"`
	Muted       []scaleDTO `json:"muted"`
	Success     []scaleDTO `json:"success"`
	Warning     []scaleDTO `json:"warning"`
	Destructive []scaleDTO `json:"destructive"`
}

// surfaceDTO is the flat chrome token set (background/foreground/card/border/ring).
type surfaceDTO struct {
	Background string `json:"background"`
	Foreground string `json:"foreground"`
	Card       string `json:"card"`
	CardFg     string `json:"cardForeground"`
	Border     string `json:"border"`
	Ring       string `json:"ring"`
}

// tokensDTO is the whole BrandTokens set flattened for the UI: the seed hex(es),
// every role ramp, light + dark surface, radius and fonts. It is what the global
// editor and the per-project resolver both render as live swatches/preview.
type tokensDTO struct {
	Primary  string     `json:"primary"`
	Accent   string     `json:"accent"`
	Roles    rolesDTO   `json:"roles"`
	Surface  surfaceDTO `json:"surface"`
	Dark     surfaceDTO `json:"dark"`
	Radius   string     `json:"radius"`
	FontSans string     `json:"fontSans"`
	FontMono string     `json:"fontMono"`
}

// scaleToDTO flattens one Scale into the canonical stop order.
func scaleToDTO(s brand.Scale) []scaleDTO {
	pairs := s.Ordered()
	out := make([]scaleDTO, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, scaleDTO{Step: p.Step, Hex: p.Hex})
	}
	return out
}

func surfaceToDTO(s brand.Surface) surfaceDTO {
	return surfaceDTO{
		Background: s.Background, Foreground: s.Foreground,
		Card: s.Card, CardFg: s.CardFg, Border: s.Border, Ring: s.Ring,
	}
}

// magentoThemeDTO is one Magento frontend theme for the brand picker: its
// Vendor/name path, title, parent, resolved role/surface swatches, and flags
// marking the Luma default and a parent-inherited (fallback) brand.
type magentoThemeDTO struct {
	Path     string     `json:"path"`
	Vendor   string     `json:"vendor"`
	Name     string     `json:"name"`
	Title    string     `json:"title"`
	Parent   string     `json:"parent"`
	Roles    rolesDTO   `json:"roles"`
	Surface  surfaceDTO `json:"surface"`
	IsLuma   bool       `json:"isLuma"`
	Fallback bool       `json:"fallback"`
}

// magentoThemeToDTO flattens a detected Magento theme for the UI. Only the role
// ramps and surface tokens that resolved are populated (a Magento var maps onto a
// role's 500 stop), so the picker renders exactly what the theme's Less defined.
func magentoThemeToDTO(t brand.MagentoTheme) magentoThemeDTO {
	return magentoThemeDTO{
		Path: t.Path, Vendor: t.Vendor, Name: t.Name,
		Title: t.Title, Parent: t.Parent,
		Roles: rolesDTO{
			Brand:       scaleToDTO(t.Roles.Brand),
			Accent:      scaleToDTO(t.Roles.Accent),
			Neutral:     scaleToDTO(t.Roles.Neutral),
			Muted:       scaleToDTO(t.Roles.Muted),
			Success:     scaleToDTO(t.Roles.Success),
			Warning:     scaleToDTO(t.Roles.Warning),
			Destructive: scaleToDTO(t.Roles.Destructive),
		},
		Surface:  surfaceToDTO(t.Surface),
		IsLuma:   t.IsLuma,
		Fallback: t.Fallback,
	}
}

// toTokensDTO serialises a full token set for the UI. Single source of shape so
// the global editor, the project resolver and any preview render identically.
func toTokensDTO(t brand.BrandTokens) tokensDTO {
	return tokensDTO{
		Primary: t.Seed.Primary,
		Accent:  t.Seed.Accent,
		Roles: rolesDTO{
			Brand:       scaleToDTO(t.Roles.Brand),
			Accent:      scaleToDTO(t.Roles.Accent),
			Neutral:     scaleToDTO(t.Roles.Neutral),
			Muted:       scaleToDTO(t.Roles.Muted),
			Success:     scaleToDTO(t.Roles.Success),
			Warning:     scaleToDTO(t.Roles.Warning),
			Destructive: scaleToDTO(t.Roles.Destructive),
		},
		Surface:  surfaceToDTO(t.Surface),
		Dark:     surfaceToDTO(t.Dark),
		Radius:   t.Radius.Base,
		FontSans: t.Font.Sans,
		FontMono: t.Font.Mono,
	}
}

// --- global default editor (Settings → Brand) -------------------------------

// handleBrandGlobal is the GLOBAL brand default editor's endpoint. GET returns
// the saved default (or a generated preview so the editor is never blank) with
// an `exists` flag. POST generates a full token set from the seed primary (+
// optional accent) and, unless `preview` is set, persists it as the house
// default every Tailwind/Bootstrap theme keel builds inherits — the `preview`
// path lets the editor render live swatches for a seed the user hasn't committed
// yet without deriving any colour client-side (the brand package stays the one
// source of the generation logic). It never writes a project — it owns only
// ~/.config/keel/brand.yaml via brand.SaveGlobal. Loopback + same-origin +
// token-guarded like every /api route.
func handleBrandGlobal(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		t, ok, err := brand.LoadGlobal()
		if err != nil {
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		if !ok {
			// No default saved yet: show a neutral preview so the editor renders
			// live swatches from the first paint. The caller sees exists=false and
			// must POST (without preview) to actually persist it.
			t, err = brand.Generate("#5b21b6", "")
			if err != nil {
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, map[string]any{"exists": false, "tokens": toTokensDTO(t)})
			return
		}
		writeJSON(w, map[string]any{"exists": true, "tokens": toTokensDTO(t)})
		return
	}

	// POST: generate from the seed(s); persist unless it's a live preview.
	var body struct {
		Primary string `json:"primary"`
		Accent  string `json:"accent"`
		Preview bool   `json:"preview"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request")
		return
	}
	tokens, err := brand.Generate(body.Primary, body.Accent)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	if body.Preview {
		// Generated but not saved: the editor renders these swatches live while the
		// user tweaks the seed. exists reflects the persisted state, not this render.
		writeJSON(w, map[string]any{"exists": brand.GlobalExists(), "preview": true, "tokens": toTokensDTO(tokens)})
		return
	}
	if err := brand.SaveGlobal(tokens); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"exists": true, "saved": true, "tokens": toTokensDTO(tokens)})
}

// --- per-project override (project view → Brand tab) ------------------------

// handleBrandProject is the PER-PROJECT brand override endpoint. GET(?dir=)
// reports everything the project's Brand tab needs to render: the existing CSS
// tokens Detect found (to pre-fill), the resolved brand (project → global → kit,
// with which source won and its seed), and whether the project carries its own
// override. POST sets or clears the override in the manifest's brand block, then
// re-resolves and applies the winning token set to the project's CSS framework
// via brand.ApplyTokens — so a "set" and a "clear" both leave the theme correct.
// projectDir gates every write to a tracked keel project.
func handleBrandProject(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		dir, err := projectDir(r.URL.Query().Get("dir"))
		if err != nil {
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		resp := map[string]any{}
		// Detect the project's own CSS tokens so the tab can pre-fill / show the
		// stack even when there is no keel override.
		if det, derr := brand.Detect(dir); derr == nil {
			resp["stack"] = det.Stack
			resp["detectedFile"] = det.File
			resp["hasKit"] = det.Found
		}
		// Magento themes brand from Less @-vars, not a CSS kit. When this is a
		// Magento project, enumerate every frontend theme and resolve each theme's
		// brand (chasing @var indirections + the <parent> chain to Luma). The DB
		// picks the active theme, which keel can't read from disk, so the UI shows
		// all themes and marks the Luma fallback. This is additive — the CSS-kit
		// fields above stay for a Magento project that also ships Tailwind/Bootstrap.
		if brand.IsMagentoProject(dir) {
			if mb, merr := brand.DetectMagento(dir); merr == nil && mb.Found {
				themes := make([]magentoThemeDTO, 0, len(mb.Themes))
				for _, th := range mb.Themes {
					themes = append(themes, magentoThemeToDTO(th))
				}
				resp["magento"] = map[string]any{
					"themes":       themes,
					"defaultIndex": mb.DefaultIndex,
				}
			}
		}
		// The project's own seed override (nil when none).
		if seed, ok := brand.ProjectOverride(dir); ok {
			resp["override"] = map[string]string{"primary": seed.Primary, "accent": seed.Accent}
		}
		// The resolved brand: which layer wins, its seed and full token set.
		res, err := brand.Resolve(dir)
		if err != nil {
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		resp["source"] = string(res.Source)
		resp["hasTokens"] = res.HasTokens
		if res.HasTokens {
			resp["resolved"] = toTokensDTO(res.Tokens)
		}
		writeJSON(w, resp)
		return
	}

	// POST: set or clear the per-project override, then apply the resolved brand,
	// plus the full no-code theming inputs (radius, font, logo, favicon) at parity
	// with the CLI. Logo/favicon arrive base64-encoded from the browser upload.
	var body struct {
		Dir         string `json:"dir"`
		Primary     string `json:"primary"`
		Accent      string `json:"accent"`
		Radius      string `json:"radius"`
		Font        string `json:"font"`
		LogoData    string `json:"logoData"` // base64 (optionally a data: URL)
		LogoName    string `json:"logoName"`
		FaviconData string `json:"faviconData"` // base64
		Clear       bool   `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request")
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	m, err := engine.ReadManifest(dir)
	if err != nil {
		writeJSON(w, map[string]string{"error": "not a keel project"})
		return
	}
	if body.Clear {
		m.Brand = nil
	} else {
		// Validate the seed before writing so a bad hex never lands in the
		// manifest. Generate is the same validator the CLI and wizard use.
		if _, gerr := brand.Generate(body.Primary, body.Accent); gerr != nil {
			writeJSON(w, map[string]string{"error": gerr.Error()})
			return
		}
		m.Brand = &engine.BrandRef{Primary: body.Primary, Accent: body.Accent}
	}
	if err := engine.WriteManifestFile(dir, m); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	// Re-resolve after the manifest change (project → global → kit) and apply the
	// winning token set to the CSS framework, so the theme matches the override
	// state. When nothing wins (kit's own colours), there is nothing to write.
	res, err := brand.Resolve(dir)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]any{"source": string(res.Source), "hasTokens": res.HasTokens}
	if res.HasTokens {
		// Full-theming overrides at parity with the CLI: radius + font.
		if body.Radius != "" {
			res.Tokens.Radius.Base = body.Radius
		}
		if body.Font != "" {
			res.Tokens.Font.Sans = body.Font
		}
		applied, aerr := brand.ApplyTokens(dir, res.Tokens)
		if aerr != nil {
			// The override is recorded, but no CSS stack to write into: report it
			// as a note, not a hard failure — the seed still resolves for a rebuild.
			resp["note"] = aerr.Error()
		} else {
			resp["stack"] = applied.Stack
			resp["file"] = applied.File
			if applied.Note != "" {
				resp["applyNote"] = applied.Note
			}
		}
		resp["resolved"] = toTokensDTO(res.Tokens)
	} else {
		resp["note"] = "no keel brand applies. The kit's own colours stand"
	}

	// Logo + favicon: no-code assets, applied whether or not colours resolved.
	logo := decodeUpload(body.LogoData)
	fav := decodeUpload(body.FaviconData)
	if len(logo) > 0 || len(fav) > 0 {
		if ar, aerr := brand.ApplyAssetsData(dir, logo, body.LogoName, fav); aerr != nil {
			resp["assetsNote"] = aerr.Error()
		} else {
			resp["assets"] = ar.Written
		}
	}
	writeJSON(w, resp)
}

// decodeUpload turns a browser upload (raw base64 or a data: URL) into bytes.
func decodeUpload(s string) []byte {
	if s == "" {
		return nil
	}
	if i := strings.Index(s, ","); strings.HasPrefix(s, "data:") && i >= 0 {
		s = s[i+1:]
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return b
}

// --- back-compat: the original two-colour per-project apply -------------------

// handleBrand is the legacy two-colour brand apply kept for back-compat (older
// clients / the CLI's `keel brand` shape). It writes primary (+ optional accent)
// straight into the project's CSS via brand.Apply. New UI uses the /global and
// /project endpoints above; this stays so nothing that spoke to /api/brand
// breaks. Loopback-only, POST-only, token-guarded, projectDir-validated.
func handleBrand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir     string `json:"dir"`
		Primary string `json:"primary"`
		Accent  string `json:"accent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, "bad request")
		return
	}
	dir, err := projectDir(body.Dir)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	res, err := brand.Apply(dir, body.Primary, body.Accent)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"stack": res.Stack, "file": res.File, "note": res.Note})
}
