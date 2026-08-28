package brand

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// assets.go handles the non-CSS half of branding: the logo and favicon. Colours,
// radius and fonts are tokens (tokens.go / render.go); a logo and favicon are
// files the framework serves. ApplyAssets copies them where each framework picks
// them up, so `keel brand --logo … --favicon …` themes a project with no code,
// mirroring what the studio's brand view sets.

// AssetResult reports the files an asset apply wrote (project-relative).
type AssetResult struct {
	Written []string
}

// ApplyAssets copies a logo and/or favicon into the project. The logo lands in
// the project's web-served dir as brand-logo.<ext>; the favicon lands at
// public/favicon.ico (the universal default) and, for a Next App Router project,
// also at app/favicon.ico where Next auto-detects it. Empty paths are skipped.
func ApplyAssets(dir, logo, favicon string) (AssetResult, error) {
	var res AssetResult
	pub := publicDir(dir)

	if logo = strings.TrimSpace(logo); logo != "" {
		ext := filepath.Ext(logo)
		if ext == "" {
			ext = ".png"
		}
		dst := filepath.Join(pub, "brand-logo"+ext)
		if err := copyFile(logo, dst); err != nil {
			return res, fmt.Errorf("logo: %w", err)
		}
		res.Written = append(res.Written, relTo(dir, dst))
	}

	if favicon = strings.TrimSpace(favicon); favicon != "" {
		dst := filepath.Join(pub, "favicon.ico")
		if err := copyFile(favicon, dst); err != nil {
			return res, fmt.Errorf("favicon: %w", err)
		}
		res.Written = append(res.Written, relTo(dir, dst))
		// Next.js App Router serves app/favicon.ico automatically.
		if app := filepath.Join(dir, "app"); isDir(app) {
			adst := filepath.Join(app, "favicon.ico")
			if err := copyFile(favicon, adst); err == nil {
				res.Written = append(res.Written, relTo(dir, adst))
			}
		}
	}
	return res, nil
}

// ApplyAssetsData is ApplyAssets for in-memory bytes (what the studio uploads via
// the browser as base64). logoName supplies the extension. Empty inputs skip.
func ApplyAssetsData(dir string, logo []byte, logoName string, favicon []byte) (AssetResult, error) {
	var res AssetResult
	pub := publicDir(dir)
	if len(logo) > 0 {
		ext := filepath.Ext(logoName)
		if ext == "" {
			ext = ".png"
		}
		dst := filepath.Join(pub, "brand-logo"+ext)
		if err := os.WriteFile(dst, logo, 0o644); err != nil {
			return res, fmt.Errorf("logo: %w", err)
		}
		res.Written = append(res.Written, relTo(dir, dst))
	}
	if len(favicon) > 0 {
		dst := filepath.Join(pub, "favicon.ico")
		if err := os.WriteFile(dst, favicon, 0o644); err != nil {
			return res, fmt.Errorf("favicon: %w", err)
		}
		res.Written = append(res.Written, relTo(dir, dst))
		if app := filepath.Join(dir, "app"); isDir(app) {
			adst := filepath.Join(app, "favicon.ico")
			if err := os.WriteFile(adst, favicon, 0o644); err == nil {
				res.Written = append(res.Written, relTo(dir, adst))
			}
		}
	}
	return res, nil
}

// publicDir returns the project's web-served directory, creating public/ if none
// of the conventional ones exist. Order matches framework conventions:
// public (Next/Laravel/Astro/Vite), static (Django/Flask).
func publicDir(dir string) string {
	for _, c := range []string{"public", "static"} {
		if isDir(filepath.Join(dir, c)) {
			return filepath.Join(dir, c)
		}
	}
	p := filepath.Join(dir, "public")
	_ = os.MkdirAll(p, 0o755)
	return p
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func relTo(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return r
	}
	return p
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
