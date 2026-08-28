package studio

// These tests guard the studio page's output-encoding, the P0 stored-XSS fix.
// index.html is served verbatim from an embedded string and builds most of its
// DOM with template-literal innerHTML, so its escaping is a security control that
// nothing else in the test suite covers. They read the embedded page and assert
// the encoding discipline holds, so a future edit that reintroduces a raw sink
// fails here rather than in a browser.

import (
	"regexp"
	"strings"
	"testing"
)

// TestEscEscapesQuotes proves esc() covers attribute contexts, not just text:
// it must map the two quote characters as well as & < >, or a value reflected
// into title="…" / onclick='…' / style="…" could break out of the attribute.
func TestEscEscapesQuotes(t *testing.T) {
	// Locate the one authoritative esc() definition and read to the end of its line.
	i := strings.Index(indexHTML, "const esc=s=>String(s).replace(")
	if i < 0 {
		t.Fatal("could not find the esc() definition in index.html")
	}
	line := indexHTML[i:]
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	// esc() must map & < > AND both quote characters, or attribute contexts
	// (title="…", onclick='…', style="…") are breakable again.
	for _, want := range []string{"&quot;", "&#39;", "&amp;", "&lt;", "&gt;"} {
		if !strings.Contains(line, want) {
			t.Errorf("esc() no longer maps to %s — attribute contexts are unescaped again: %s", want, line)
		}
	}
	// Its character class must include both quote characters.
	if !strings.Contains(line, `[&<>"']`) {
		t.Errorf("esc()'s character class does not include both quote characters: %s", line)
	}
}

// TestNoRawJSONStringifyInInlineHandlers proves every untrusted value passed to an
// inline handler goes through jarg (esc∘JSON.stringify), never a bare
// JSON.stringify. A bare one inside onclick='…' is broken out of by a value
// containing a single quote — the exact sink the P0 fix closed. jarg's own
// definition is the only allowed esc(JSON.stringify(…)).
func TestNoRawJSONStringifyInInlineHandlers(t *testing.T) {
	// Any inline event handler attribute (onclick / oninput / onchange / onkeydown /
	// onblur …), single- OR double-quoted, containing a raw ${JSON.stringify( .
	handler := regexp.MustCompile(`on\w+=(['"])[^'"]*\$\{JSON\.stringify\(`)
	if locs := handler.FindAllString(indexHTML, -1); len(locs) > 0 {
		t.Errorf("found %d inline handler(s) with a raw JSON.stringify (use jarg): %q", len(locs), locs[0])
	}
	// The jarg helper must exist, since the sinks depend on it.
	if !strings.Contains(indexHTML, "const jarg=x=>esc(JSON.stringify(x));") {
		t.Error("the jarg() helper is missing; the escaped-argument sinks have no definition")
	}
}

// TestPluginHrefSchemeIsValidated proves a plugin-contributed link is scheme-checked
// (http/https only) before it lands in an href, so a javascript:/data: URL cannot
// run on click. esc() stops the value breaking the attribute; safeURL() stops the
// scheme.
func TestPluginHrefSchemeIsValidated(t *testing.T) {
	if !strings.Contains(indexHTML, "const safeURL=") {
		t.Fatal("safeURL() helper is missing; plugin hrefs are not scheme-validated")
	}
	// The two plugin/link href sinks must route through safeURL.
	for _, want := range []string{
		`href="${esc(safeURL(it.href))}"`,
		`href="${esc(safeURL(p.homepage))}"`,
	} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("a plugin href sink is not validated through safeURL: expected %q", want)
		}
	}
}

// TestBrandColorGatedBeforeStyle proves brand token colours are validated with
// cssColor() (which falls back to a valid hex or transparent) before being written
// into a style="" attribute, so a crafted colour token cannot inject extra CSS.
func TestBrandColorGatedBeforeStyle(t *testing.T) {
	if !strings.Contains(indexHTML, "const cssColor=") {
		t.Fatal("cssColor() helper is missing; brand colours reach style= unvalidated")
	}
	// No colour token may reach a style="" attribute without cssColor(). Match
	// background:/color:/border-color: interpolations that pull a token field.
	rawColor := regexp.MustCompile(`style="[^"]*(?:background|color|border-color):\$\{esc\((?:s\.hex|hex|s\.card|s\.foreground|s\.border|pri|acc)\)\}`)
	if locs := rawColor.FindAllString(indexHTML, -1); len(locs) > 0 {
		t.Errorf("brand colour reaches style= without cssColor gating: %q", locs[0])
	}
}
