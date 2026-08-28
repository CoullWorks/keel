import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { apiJSON, fetchJSON } from '../../lib/api'
import { navTo } from '../../lib/router'
import type { Project, Member, Backend } from '../../lib/types'

/* ---------------------------------------------------------------------------
   Per-project BRAND tab — the React port of renderBrandProject (index.html
   §3015-3172) and every helper it calls: paintBrandProject, srcLabel,
   pbSeed/previewBrandProject (debounced live preview), saveBrandProject,
   clearBrandProject, pbFile (logo/favicon → base64), and the Magento theme
   picker (magentoThemesCard / pickMagentoTheme).

   It edits THIS project's brand override — a seed primary (+ optional accent).
   With no override the project inherits the house default (Settings → Brand),
   so the seed fields pre-fill with the resolved seed and read "inherit". A
   source pill says which layer wins (project / global / kit).

   The generated 50-950 scales, roles, surface and preview are the SAME token
   vocabulary as the global editor. The studio never derives a colour itself:
   the live preview asks the server to Generate (POST /api/brand/global with
   preview:true — never saves), byte-identical to what Save persists, exactly
   like the original previewBrandProject. Save/clear go to /api/brand/project.

   The exact studio.css classes are reused verbatim. React escapes text and
   attributes, so the original esc()/jarg() calls fall away; the one
   security-critical guard that survives is cssColor(), which drops any non-hex
   token to "transparent" before it lands in an inline style.

   Endpoints (confirmed against internal/studio/brand.go):
     GET  /api/brand/project?dir=<p.path>
            → { stack?, detectedFile?, hasKit?, magento?, override?,
                source, hasTokens, resolved? }
     POST /api/brand/global  { primary, accent, preview:true }   (live preview)
            → { tokens }
     POST /api/brand/project { dir, primary, accent, radius, font,
                               logoData, logoName, faviconData }  (set & apply)
     POST /api/brand/project { dir, clear:true }                 (clear override)
            → { source, hasTokens, stack?, file?, applyNote?, note?, assets?,
                resolved? } | { error }
--------------------------------------------------------------------------- */

type Focus = Project | Member

// Effective (possibly inherited) backend — part of the tab contract; unused
// here (this tab reads the brand endpoints, not the backend), redefined local
// to match ProjectDetail's own lax Backend type.
// ---------- brand token DTOs (mirrors brand.go tokensDTO / rolesDTO / etc.) --
type Scale = { step: number; hex: string }
type Surface = {
  background: string
  foreground: string
  card: string
  cardForeground: string
  border: string
  ring: string
}
type Roles = {
  brand: Scale[]
  accent: Scale[]
  neutral: Scale[]
  muted: Scale[]
  success: Scale[]
  warning: Scale[]
  destructive: Scale[]
}
type Tokens = {
  primary: string
  accent: string
  roles: Roles
  surface: Surface
  dark: Surface
  radius: string
  fontSans: string
  fontMono: string
}
// A Magento frontend theme (magentoThemeDTO). Its role ramps carry only what the
// theme's Less resolved; surface tokens likewise.
type MagentoTheme = {
  path: string
  vendor?: string
  name?: string
  title?: string
  parent?: string
  roles: Roles
  surface: Surface
  isLuma?: boolean
  fallback?: boolean
}
type Magento = { themes: MagentoTheme[]; defaultIndex?: number }
// The full GET /api/brand/project answer.
type ProjectBrand = {
  stack?: string
  detectedFile?: string
  hasKit?: boolean
  magento?: Magento
  override?: { primary?: string; accent?: string }
  source?: string
  hasTokens?: boolean
  resolved?: Tokens
  error?: string
}
// The POST /api/brand/project (save) answer.
type SaveResp = {
  source?: string
  hasTokens?: boolean
  stack?: string
  file?: string
  applyNote?: string
  note?: string
  assets?: string[]
  assetsNote?: string
  resolved?: Tokens
  error?: string
}
type PreviewResp = { tokens?: Tokens; error?: string }

// ---------- shared helpers ported verbatim ----------
const hexOK = (s?: string) => /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(String(s || '').trim())
// cssColor gates a colour token before it lands in a style value. Anything not a
// valid hex is dropped to "transparent" so a crafted value can neither break the
// attribute nor inject extra CSS declarations.
const cssColor = (v?: string) => (hexOK(v) ? String(v).trim() : 'transparent')
// isLightHex decides label contrast on a swatch (crude luminance, enough for a caption).
function isLightHex(hex?: string): boolean {
  const h = String(hex || '').replace('#', '')
  if (h.length !== 6 && h.length !== 3) return false
  const x = h.length === 3 ? h.split('').map((c) => c + c).join('') : h
  const r = parseInt(x.slice(0, 2), 16),
    g = parseInt(x.slice(2, 4), 16),
    b = parseInt(x.slice(4, 6), 16)
  return 0.299 * r + 0.587 * g + 0.114 * b > 150
}

// ============================================================================
// Shared token renderers — the one vocabulary for every brand preview
// (scaleHTML / surfaceHTML / previewHTML / tokensPanel), ported verbatim.
// ============================================================================

function ScaleHTML({ name, stops }: { name: string; stops: Scale[] }) {
  if (!stops || !stops.length) return null
  return (
    <div className="brandrole">
      <div className="rname">{name}</div>
      <div className="scale">
        {stops.map((s) => (
          <div
            key={s.step}
            className={'stop' + (isLightHex(s.hex) ? ' lt' : '')}
            style={{ background: cssColor(s.hex) }}
            title={`${name}-${s.step}: ${s.hex}`}
          >
            <small>{s.step}</small>
          </div>
        ))}
      </div>
    </div>
  )
}

function SurfaceHTML({ title, sf }: { title: string; sf: Surface }) {
  const toks: [string, string][] = [
    ['background', sf.background],
    ['foreground', sf.foreground],
    ['card', sf.card],
    ['card-fg', sf.cardForeground],
    ['border', sf.border],
    ['ring', sf.ring],
  ]
  return (
    <div className="brandrole">
      <div className="rname">{title}</div>
      <div className="roleflat">
        {toks.map(([label, hex]) =>
          hex ? (
            <span key={label} className="tok">
              <i style={{ background: cssColor(hex) }} />
              {label}
            </span>
          ) : null,
        )}
      </div>
    </div>
  )
}

// previewHTML paints a tiny themed card in light and in dark from the tokens, so
// the effect of the seed is visible, not just the swatches.
function PreviewHTML({ t }: { t: Tokens }) {
  const sf = t.surface,
    dk = t.dark,
    brand = t.roles.brand || [],
    accent = t.roles.accent || []
  const at = (w: number) => (brand.find((x) => x.step === w) || ({} as Scale)).hex || '#5b21b6'
  const ac = (w: number) => (accent.find((x) => x.step === w) || ({} as Scale)).hex || at(w)
  const card = (mode: string, s: Surface, pri: string, acc: string) => (
    <div
      className="pvcard"
      style={{ background: cssColor(s.card), color: cssColor(s.foreground), borderColor: cssColor(s.border) }}
    >
      <div className="pvmode">{mode}</div>
      <h4>Aa: themed card</h4>
      <p style={{ color: cssColor(s.foreground) }}>Buttons, borders and text derive from your seed.</p>
      <div className="pvbtns">
        <button className="pvbtn" style={{ background: cssColor(pri), color: '#fff' }}>
          Primary
        </button>
        <button
          className="pvbtn"
          style={{ background: 'transparent', color: cssColor(acc), border: `1px solid ${cssColor(acc)}` }}
        >
          Accent
        </button>
      </div>
    </div>
  )
  return (
    <div className="brandprev">
      {card('Light', sf, at(600), ac(600))}
      {card('Dark', dk, at(500), ac(400))}
    </div>
  )
}

// tokensPanel renders the full generated set: roles, surface (light+dark),
// radius/font, and the preview.
function TokensPanel({ t }: { t: Tokens }) {
  const order: [string, Scale[]][] = [
    ['brand', t.roles.brand],
    ['accent', t.roles.accent],
    ['neutral', t.roles.neutral],
    ['muted', t.roles.muted],
    ['success', t.roles.success],
    ['warning', t.roles.warning],
    ['destructive', t.roles.destructive],
  ]
  return (
    <>
      <div className="msub" style={{ marginTop: 16 }}>
        Semantic roles: 50 → 950
      </div>
      {order.map(([n, s]) => (
        <ScaleHTML key={n} name={n} stops={s} />
      ))}
      <SurfaceHTML title="Surface: light" sf={t.surface} />
      <SurfaceHTML title="Surface: dark" sf={t.dark} />
      <div className="row" style={{ gap: 14, flexWrap: 'wrap', marginTop: 14, fontSize: 12, color: 'var(--dim)' }}>
        <span>
          radius <code>{t.radius || '—'}</code>
        </span>
        <span>
          sans{' '}
          <code style={{ fontSize: 11 }}>{(t.fontSans || '—').split(',')[0]}</code>
        </span>
        <span>
          mono{' '}
          <code style={{ fontSize: 11 }}>{(t.fontMono || '—').split(',')[0]}</code>
        </span>
      </div>
      <div className="msub" style={{ marginTop: 16 }}>
        Preview
      </div>
      <PreviewHTML t={t} />
    </>
  )
}

// ============================================================================
// srcLabel — the source pill: which layer wins for this project.
// ============================================================================
function SrcLabel({ src, hasKit }: { src?: string; hasKit?: boolean }) {
  if (src === 'project')
    return (
      <span className="srcpill project">
        <i />
        this project's own override
      </span>
    )
  if (src === 'global')
    return (
      <span className="srcpill global">
        <i />
        inheriting the global default
      </span>
    )
  return (
    <span className="srcpill">
      <i />
      {hasKit ? "the kit's own colours (no keel brand)" : 'no CSS kit detected'}
    </span>
  )
}

// ============================================================================
// Magento theme picker (magentoThemesCard / pickMagentoTheme) — every frontend
// theme keel found, its resolved swatches, and a marker for the Luma fallback.
// Returns nothing for a non-Magento project so the card only appears where it
// makes sense.
// ============================================================================
function MagentoThemesCard({ mg }: { mg?: Magento }) {
  const themes = mg?.themes || []
  const init = mg && typeof mg.defaultIndex === 'number' && mg.defaultIndex >= 0 ? mg.defaultIndex : 0
  const [sel, setSel] = useState(init)
  // Re-seat the selection if the project (and so its magento block) changes.
  useEffect(() => {
    setSel(init)
  }, [mg])
  if (!themes.length) return null
  const t = themes[sel]
  return (
    <div className="card" style={{ marginTop: 14 }}>
      <div className="row" style={{ justifyContent: 'space-between', alignItems: 'flex-start', gap: 12, flexWrap: 'wrap' }}>
        <div>
          <h3 style={{ margin: '0 0 4px' }}>Magento theme brand</h3>
          <p className="muted" style={{ fontSize: 12.5, margin: 0, maxWidth: '62ch' }}>
            Brand read from each theme's Less vars under <code>web/css/source/</code> (chasing <code>@var</code>{' '}
            indirections and the <code>&lt;parent&gt;</code> chain to Luma). Magento picks the active theme in the
            database, which keel can't read from disk, so every theme is shown and the <b>Luma default</b> is the
            fallback.
          </p>
        </div>
      </div>
      <div className="opts" style={{ marginTop: 12, gap: 8 }}>
        {themes.map((th, i) => (
          <button
            key={i}
            className={'btn sm mtheme-btn' + (i === sel ? ' primary' : '')}
            data-i={i}
            onClick={() => setSel(i)}
          >
            {th.title || th.path} {th.isLuma ? <span className="tag dim">default</span> : ''}
          </button>
        ))}
      </div>
      <div id="mtheme_panel" style={{ marginTop: 12 }}>
        {t && <MagentoThemePanel t={t} />}
      </div>
    </div>
  )
}

// pickMagentoTheme's body: the selected theme's resolved role/surface swatches,
// reusing the shared renderers so a Magento theme's brand shows in the exact same
// vocabulary as every other brand surface.
function MagentoThemePanel({ t }: { t: MagentoTheme }) {
  const meta = [
    t.parent ? (
      <span key="parent">
        parent <code>{t.parent}</code>
      </span>
    ) : null,
    t.fallback ? (
      <span key="fb" className="tag warn">
        brand inherited from parent
      </span>
    ) : null,
    t.isLuma ? (
      <span key="luma" className="tag dim">
        Luma: the on-disk default
      </span>
    ) : null,
  ].filter(Boolean)
  const roles: [string, Scale[]][] = [
    ['brand', t.roles.brand],
    ['accent', t.roles.accent],
    ['success', t.roles.success],
    ['warning', t.roles.warning],
    ['destructive', t.roles.destructive],
  ]
  const scales = roles.filter(([, s]) => s && s.length)
  const sf = t.surface
  const hasSurface = !!(sf && (sf.background || sf.foreground || sf.card || sf.border))
  return (
    <>
      <div className="muted" style={{ fontSize: 12, margin: '0 0 8px' }}>
        {t.path}
        {meta.length ? (
          <>
            {' · '}
            {meta.map((m, i) => (
              <span key={i}>
                {i ? ' · ' : ''}
                {m}
              </span>
            ))}
          </>
        ) : null}
      </div>
      {scales.length || hasSurface ? (
        <>
          {scales.map(([n, s]) => (
            <ScaleHTML key={n} name={n} stops={s} />
          ))}
          {hasSurface && <SurfaceHTML title="Surface" sf={sf} />}
        </>
      ) : (
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
          No brand Less vars resolved for this theme.
        </p>
      )}
    </>
  )
}

// ============================================================================
// The per-project Brand tab.
// ============================================================================
export default function BrandTab({ p, be }: { p: Project | Member; be: Backend | null }) {
  void be // part of the tab contract; this tab reads the brand endpoints only
  const dir = p.path

  // GET /api/brand/project?dir= — keyed by the project path so switching focus
  // refetches. fetchJSON returns {error} rather than throwing (the original's
  // renderBrandProject fetchJSON), so we render the error inline.
  const {
    data: raw,
    isLoading,
    isError,
    error,
  } = useQuery({
    queryKey: ['brand-project', dir],
    queryFn: () => fetchJSON<ProjectBrand>('/api/brand/project?dir=' + encodeURIComponent(dir)),
    retry: false,
  })

  // "resolving this project's brand…" while the GET is in flight (renderBrandProject).
  if (isLoading || (!raw && !isError)) {
    return (
      <div className="card">
        <p className="muted" style={{ margin: 0 }}>
          resolving this project's brand…
        </p>
      </div>
    )
  }
  // A thrown fetch (fetchJSON never throws, but keep the guard) or an {error}
  // payload → the calm inline error, exactly like the original.
  if (isError) return <div className="err">{String((error as Error)?.message || error)}</div>
  const d = raw as ProjectBrand
  if (d.error) return <div className="err">{d.error}</div>

  // Remount the editor when focus moves so its seed state re-seeds from the new
  // project's GET (the original threw the whole DOM away and re-ran
  // renderBrandProject; keying does the same for the local state).
  return <BrandEditor key={dir} p={p as Focus} d={d} />
}

function BrandEditor({ p, d }: { p: Focus; d: ProjectBrand }) {
  const qc = useQueryClient()
  const dir = p.path
  const hasOverride = !!d.override

  // Pre-fill the seed fields: the project's own override if it has one, else the
  // resolved seed (global default / detected), else clear (placeholder shows
  // "inherit"). Ported verbatim from renderBrandProject.
  const ov = d.override
  const [primary, setPrimary] = useState((ov && ov.primary) || (d.resolved && d.resolved.primary) || '')
  const [accent, setAccent] = useState((ov && ov.accent) || (d.resolved && d.resolved.accent) || '')
  const [radius, setRadius] = useState('')
  const [font, setFont] = useState('')

  // Logo/favicon read to base64 for the POST (no upload server; the studio is
  // loopback and writes into the project directly). pbFile.
  const logoData = useRef('')
  const logoName = useRef('')
  const faviconData = useRef('')

  // The resolved-brand panel: seeded from the GET's resolved tokens, overwritten
  // live by the preview (previewBrandProject) so it shows what a Save would apply.
  const [panelTokens, setPanelTokens] = useState<Tokens | null>(d.resolved || null)
  const [out, setOut] = useState<React.ReactNode>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // previewBrandProject — show the seed's generated theme live (Generate via the
  // global preview path — never saves, never derives client-side) before the user
  // commits it as this project's override.
  const previewBrandProject = async (pri: string, acc: string) => {
    if (!hexOK(pri)) return
    if (acc && !hexOK(acc)) return
    let r: PreviewResp | null = null
    try {
      r = await apiJSON<PreviewResp>('/api/brand/global', { primary: pri, accent: acc, preview: true })
    } catch {
      r = null
    }
    if (!r || r.error || !r.tokens) return
    setPanelTokens(r.tokens)
  }

  // pbSeed — update a seed and re-generate the preview live (debounced 220ms).
  const pbSeed = (k: 'primary' | 'accent', v: string) => {
    const val = v.trim()
    const nextP = k === 'primary' ? val : primary
    const nextA = k === 'accent' ? val : accent
    if (k === 'primary') setPrimary(val)
    else setAccent(val)
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => previewBrandProject(nextP, nextA), 220)
  }

  const pbFile = (files: FileList | null, kind: 'logo' | 'favicon') => {
    const f = files && files[0]
    if (!f) return
    // Cap the upload before reading it: a data URL inflates the file ~33% into a
    // single in-memory string that is POSTed in the JSON body, so an oversized (or
    // mis-selected non-image) file is a needless memory hit. A logo/favicon is
    // tiny; 2 MB is generous. The server remains the real validator of the bytes.
    const maxBytes = 2 * 1024 * 1024
    if (f.size > maxBytes) {
      setOut(
        <span className="err">
          {f.name} is {(f.size / 1024 / 1024).toFixed(1)} MB — please choose an image under 2 MB.
        </span>,
      )
      return
    }
    const rd = new FileReader()
    rd.onload = () => {
      if (kind === 'logo') {
        logoData.current = String(rd.result || '')
        logoName.current = f.name
      } else {
        faviconData.current = String(rd.result || '')
      }
    }
    rd.readAsDataURL(f)
  }

  // saveBrandProject — validate the seed, POST the override + no-code inputs,
  // report what applied, then refetch so the source pill + resolved panel refresh.
  const saveBrandProject = async () => {
    if (!hexOK(primary)) {
      setOut(
        <div className="err" style={{ marginTop: 12 }}>
          Primary must be a hex colour like #5b21b6.
        </div>,
      )
      return
    }
    if (accent && !hexOK(accent)) {
      setOut(
        <div className="err" style={{ marginTop: 12 }}>
          Accent must be a hex colour like #f97316 (or leave it blank).
        </div>,
      )
      return
    }
    setOut(
      <div className="muted" style={{ marginTop: 12 }}>
        writing the override and applying the theme…
      </div>,
    )
    const r = await fetchJSON<SaveResp>('/api/brand/project', {
      dir,
      primary,
      accent,
      radius,
      font,
      logoData: logoData.current,
      logoName: logoName.current,
      faviconData: faviconData.current,
    })
    if ((r as SaveResp).error) {
      setOut(
        <div className="err" style={{ marginTop: 12 }}>
          {(r as SaveResp).error}
        </div>,
      )
      return
    }
    const s = r as SaveResp
    const applied = s.stack ? `✓ ${s.source}: wrote ${s.file}` : `✓ override saved (${s.source})`
    const note = s.applyNote || s.note
    setOut(
      <>
        <div className="tag ok" style={{ marginTop: 12 }}>
          {applied}
        </div>
        {s.assets && s.assets.length ? (
          <div className="tag ok" style={{ marginTop: 6 }}>
            ✓ assets: {s.assets.join(', ')}
          </div>
        ) : null}
        {note ? (
          <div className="muted" style={{ marginTop: 6, fontSize: 12 }}>
            {note}
          </div>
        ) : null}
      </>,
    )
    // Refresh source pill + resolved panel (the original re-ran renderBrandProject).
    qc.invalidateQueries({ queryKey: ['brand-project', dir] })
  }

  // clearBrandProject — drop the override (inherit global), then refetch.
  const clearBrandProject = async () => {
    setOut(
      <div className="muted" style={{ marginTop: 12 }}>
        clearing the override…
      </div>,
    )
    const r = await fetchJSON<SaveResp>('/api/brand/project', { dir, clear: true })
    if ((r as SaveResp).error) {
      setOut(
        <div className="err" style={{ marginTop: 12 }}>
          {(r as SaveResp).error}
        </div>,
      )
      return
    }
    qc.invalidateQueries({ queryKey: ['brand-project', dir] })
  }

  const stackNote = d.hasKit ? (
    <>
      Detected <b>{d.stack || 'a CSS kit'}</b>
      {d.detectedFile ? (
        <>
          {' '}
          in <code>{d.detectedFile}</code>
        </>
      ) : null}
      .
    </>
  ) : (
    'No Tailwind or Bootstrap kit found in this project. Add a UI kit first, then a brand will apply.'
  )

  return (
    <>
      <div className="card">
        <div
          className="row"
          style={{ justifyContent: 'space-between', alignItems: 'flex-start', gap: 12, flexWrap: 'wrap' }}
        >
          <div>
            <h3 style={{ margin: '0 0 4px' }}>Brand for {p.name}</h3>
            <p className="muted" style={{ fontSize: 12.5, margin: 0, maxWidth: '60ch' }}>
              An override just for this project. Leave it clear to inherit the{' '}
              <a
                href="#"
                onClick={(e) => {
                  e.preventDefault()
                  navTo('#/settings/brand')
                }}
                style={{ color: 'var(--orange)' }}
              >
                house default
              </a>
              . Setting a seed writes it to this project's manifest and applies the generated theme to its CSS.
            </p>
          </div>
          <SrcLabel src={d.source} hasKit={d.hasKit} />
        </div>
        <p className="muted" style={{ fontSize: 12, margin: '10px 0 0' }}>
          {stackNote}
        </p>
        <div className="seedrow" style={{ marginTop: 14 }}>
          <div className="seedfld">
            <label>Primary (seed)</label>
            <div className="swatchbox">
              <input
                type="color"
                id="pb_pc"
                value={hexOK(primary) ? primary : '#5b21b6'}
                onInput={(e) => pbSeed('primary', (e.target as HTMLInputElement).value)}
              />
              <input
                type="text"
                id="pb_pt"
                value={primary}
                placeholder="inherit"
                onInput={(e) => pbSeed('primary', (e.target as HTMLInputElement).value)}
              />
            </div>
          </div>
          <div className="seedfld">
            <label>Accent (optional)</label>
            <div className="swatchbox">
              <input
                type="color"
                id="pb_ac"
                value={hexOK(accent) ? accent : '#f97316'}
                onInput={(e) => pbSeed('accent', (e.target as HTMLInputElement).value)}
              />
              <input
                type="text"
                id="pb_at"
                value={accent}
                placeholder="auto"
                onInput={(e) => pbSeed('accent', (e.target as HTMLInputElement).value)}
              />
            </div>
          </div>
          <div className="seedfld">
            <label>&nbsp;</label>
            <div className="row" style={{ gap: 8 }}>
              <button className="btn primary" onClick={saveBrandProject}>
                Set override &amp; apply
              </button>
              {hasOverride && (
                <button className="btn ghost" onClick={clearBrandProject}>
                  Clear override (inherit global)
                </button>
              )}
            </div>
          </div>
        </div>
        <div className="msub" style={{ marginTop: 14 }}>
          Full theming (no code)
        </div>
        <div className="seedrow" style={{ marginTop: 8 }}>
          <div className="seedfld">
            <label>Corner radius</label>
            <input
              type="text"
              id="pb_radius"
              value={radius}
              placeholder="0.5rem"
              onInput={(e) => setRadius((e.target as HTMLInputElement).value.trim())}
            />
          </div>
          <div className="seedfld">
            <label>Sans font</label>
            <input
              type="text"
              id="pb_font"
              value={font}
              placeholder="Inter, system-ui"
              onInput={(e) => setFont((e.target as HTMLInputElement).value.trim())}
            />
          </div>
          <div className="seedfld">
            <label>Logo</label>
            <input type="file" id="pb_logo" accept="image/*" onChange={(e) => pbFile(e.target.files, 'logo')} />
          </div>
          <div className="seedfld">
            <label>Favicon</label>
            <input type="file" id="pb_fav" accept="image/*" onChange={(e) => pbFile(e.target.files, 'favicon')} />
          </div>
        </div>
        <div id="pb_out">{out}</div>
        <div className="msub" style={{ marginTop: 16 }}>
          Resolved brand: what this project uses now
        </div>
        <div id="pb_panel">
          {panelTokens ? (
            <TokensPanel t={panelTokens} />
          ) : (
            <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
              {d.hasKit
                ? 'No keel brand applies yet. Set an override above or a global default.'
                : 'Add a UI kit, then set a brand.'}
            </p>
          )}
        </div>
      </div>
      <MagentoThemesCard mg={d.magento} />
    </>
  )
}
