// safeURL is the single guard for any href that comes from plugin- or
// user-supplied data. It resolves the input against the current location and
// keeps it ONLY when it parses as http(s); a javascript:/data:/unparseable scheme
// collapses to an inert "#" so a link can never execute on click. React escapes
// the text of a node; this closes the one hole React does not — an href attribute.
//
// It returns the PARSED, normalized href (p.href), not the raw input, so the
// emitted attribute is exactly what was validated — a parser-vs-browser
// discrepancy (odd encodings, control characters that new URL tolerates) can't
// pass through verbatim. It lives here, shared, so a fix is made once rather than
// drifting across the copies that used to exist in pluginview/Plugins/Build.
export function safeURL(u: string): string {
  try {
    const p = new URL(String(u), location.href)
    return p.protocol === 'http:' || p.protocol === 'https:' ? p.href : '#'
  } catch {
    return '#'
  }
}
