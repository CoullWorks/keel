import { useEffect, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiJSON, fetchJSON, TOKEN } from '../lib/api'
import { recipesResponse } from '../lib/types'
import { navTo } from '../lib/router'
import { clickable } from '../lib/a11y'

/* ---------------------------------------------------------------------------
   Settings surface — ported 1:1 from renderSettings (index.html §2797-3014)
   including connectCard, the profile defaults list, and the full house-brand
   editor (load/save + live scale/color preview). The exact studio.css classes
   are reused verbatim. React escapes text/attributes for us, so the original
   esc()/jarg() calls fall away; the one security-critical guard that survives
   is cssColor(), which drops any non-hex token to "transparent" before it lands
   in an inline style — a crafted brand value can neither break the attribute nor
   inject a declaration.

   Two edit flows the original reaches from here — editField() (edit one default)
   and openOnboarding() (walk the steps) — drive the un-ported build/onboarding
   recipe-selection engine (sel/frameworks()/forFw()/obChip()/defaults()). They
   live with the Build wizard (Phase 2), so those affordances route to #/build,
   matching how App.tsx stubs the other not-yet-ported views. The visible markup
   is unchanged.
--------------------------------------------------------------------------- */

// ---------- profile types (mirrors /api/profile GET) ----------
type Profile = {
  exists?: boolean
  name?: string
  projects_dir?: string
  hosting?: string
  framework?: string
  env?: string
  database?: string
  editor?: string
  frontend?: string
  webserver?: string
  services?: string
  addons?: string
  extras?: string
  hostingOptions?: { key: string; label: string }[]
}

// ---------- brand token DTOs (mirrors /api/brand/global) ----------
type Scale = { step: number; hex: string }
type Surface = {
  background: string
  foreground: string
  card: string
  cardForeground: string
  border: string
  ring: string
}
type Tokens = {
  primary: string
  accent: string
  roles: {
    brand: Scale[]
    accent: Scale[]
    neutral: Scale[]
    muted: Scale[]
    success: Scale[]
    warning: Scale[]
    destructive: Scale[]
  }
  surface: Surface
  dark: Surface
  radius: string
  fontSans: string
  fontMono: string
}
type BrandResp = { exists?: boolean; preview?: boolean; tokens?: Tokens; error?: string }

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
const csv = (s?: string) => (s || '').split(',').map((x) => x.trim()).filter(Boolean)

// ============================================================================
// PROFILE TAB
// ============================================================================

// The recipe catalogue keys labelFor/labelsFor — the same list Build reads.
function useRecipeLabels() {
  const q = useQuery({
    queryKey: ['recipes'],
    queryFn: () => apiJSON('/api/recipes').then((d) => recipesResponse.parse(d)),
  })
  const recipes = (q.data?.recipes || []) as { id: string; label?: string }[]
  const labelFor = (id?: string) => {
    const r = recipes.find((x) => x.id === id)
    return r ? r.label || '' : id || ''
  }
  const labelsFor = (list?: string) => csv(list).map(labelFor).join(', ')
  return { labelFor, labelsFor }
}

function Row({ k, fkey, v }: { k: string; fkey: string; v: string }) {
  return (
    <div
      className="plan-row"
      style={{ cursor: 'pointer', alignItems: 'center' }}
      {...clickable(() => navTo('#/build'))}
      title="click to edit"
    >
      <div className="k" style={{ width: 140 }}>
        {k}
      </div>
      <div style={{ flex: 1 }}>{v ? v : <span className="muted">default</span>}</div>
      <span className="muted" style={{ fontSize: 12 }}>
        edit ›
      </span>
    </div>
  )
}

function ProfileTab() {
  const { data: profile, error } = useQuery({
    queryKey: ['profile'],
    queryFn: () => apiJSON<Profile>('/api/profile'),
  })
  const { labelFor, labelsFor } = useRecipeLabels()

  if (error) return <div className="err">{String((error as Error).message || error)}</div>
  if (!profile) return <div className="muted">Loading…</div>

  const hostLabel = (k?: string) => {
    const h = (profile.hostingOptions || []).find((o) => o.key === k)
    return h ? h.label : k || ''
  }

  return (
    <>
      <p className="muted" style={{ fontSize: 12.5, margin: '-6px 0 16px' }}>
        Click any default to change just that one. Stored in <code>~/.config/keel/profile.yaml</code>; the CLI reads the
        same file (<code>keel init</code> edits it too).
      </p>
      <Row k="Name" fkey="name" v={profile.name || ''} />
      <Row k="Projects folder" fkey="projects_dir" v={profile.projects_dir || 'current directory'} />
      <Row k="Framework" fkey="framework" v={labelFor(profile.framework)} />
      <Row k="Environment" fkey="env" v={labelFor(profile.env)} />
      <Row k="Database" fkey="database" v={labelFor(profile.database)} />
      <Row k="Frontend" fkey="frontend" v={profile.frontend ? labelFor(profile.frontend) : 'none'} />
      <Row
        k="Web server"
        fkey="webserver"
        v={profile.webserver === 'none' ? 'none' : labelFor(profile.webserver) || 'default'}
      />
      <Row k="Services" fkey="services" v={labelsFor(profile.services)} />
      <Row k="Add-ons" fkey="addons" v={labelsFor(profile.addons)} />
      <Row k="Extras" fkey="extras" v={labelsFor(profile.extras)} />
      <Row k="Hosting" fkey="hosting" v={hostLabel(profile.hosting)} />
      <div className="row" style={{ marginTop: 20 }}>
        <button className="btn" onClick={() => navTo('#/build')}>
          Edit all (walk the steps)
        </button>
      </div>
      <ConnectCard />
    </>
  )
}

// connectCard — "Connect keel to your MCP client": the two ways to give an AI
// coding agent MCP access to keel. The live HTTP form points at this studio's own
// /mcp, guarded by this session's token; the stdio form always works. The values
// are the standard MCP server-config shapes, so they paste into any MCP client.
function ConnectCard() {
  const host = location.host || '127.0.0.1:7373'
  const httpCmd = `{ "url": "http://${host}/mcp", "headers": { "X-Keel-Token": "${TOKEN}" } }`
  const stdioCmd = `{ "command": "keel", "args": ["mcp"] }`
  return (
    <div className="card" style={{ marginTop: 22 }}>
      <h3 style={{ marginTop: 0 }}>Connect keel to your MCP client</h3>
      <p className="muted" style={{ fontSize: 12.5, margin: '0 0 12px' }}>
        Give an AI coding agent MCP access to keel. It can list frameworks and recipes, resolve a plan, inspect projects
        and generate code. Add one of these to your MCP client's config:
      </p>
      <div className="msub">Live HTTP (this studio)</div>
      <p className="muted" style={{ fontSize: 12, margin: '-2px 0 6px' }}>
        Uses the endpoint this studio auto-started at <code>http://{host}/mcp</code>, guarded by this session's token.
        Runs only while the studio is open; the token is regenerated each launch.
      </p>
      <CopyRow cmd={httpCmd} />
      <div className="msub" style={{ marginTop: 14 }}>
        Stdio (always available)
      </div>
      <p className="muted" style={{ fontSize: 12, margin: '-2px 0 6px' }}>
        Runs <code>keel mcp</code> on demand, no studio needed. Add <code>--write</code> to <code>keel mcp</code> to
        expose the write tools.
      </p>
      <CopyRow cmd={stdioCmd} />
      <p className="muted" style={{ fontSize: 11.5, margin: '12px 0 0' }}>
        Started the studio with <code>--no-mcp</code>? The live HTTP endpoint is off; use the stdio form.
      </p>
    </div>
  )
}

// copyRow renders a monospaced command with a copy button. The command carries a
// live session token, so it is never logged — only shown, and copied on demand.
function CopyRow({ cmd }: { cmd: string }) {
  const [label, setLabel] = useState('Copy')
  const copy = () => {
    const done = () => {
      setLabel('Copied')
      setTimeout(() => setLabel('Copy'), 1200)
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(cmd).then(done, done)
    } else {
      const ta = document.createElement('textarea')
      ta.value = cmd
      document.body.appendChild(ta)
      ta.select()
      try {
        document.execCommand('copy')
      } catch {
        // ignore
      }
      ta.remove()
      done()
    }
  }
  return (
    <div className="row" style={{ gap: 8, alignItems: 'stretch' }}>
      <code className="cmdbox">{cmd}</code>
      <button className="btn sm" onClick={copy}>
        {label}
      </button>
    </div>
  )
}

// ============================================================================
// BRAND TAB — shared token renderers (one vocabulary for the whole preview)
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

// ---------- Settings → the GLOBAL default editor ----------
// The house default over ~/.config/keel/brand.yaml. Choose a seed primary
// (+ optional accent); the 50-950 scales, roles, surface, radius/font and dark
// regenerate live (server-side, preview:true — the studio never derives a
// colour); Save persists it as the global default.
function BrandTab() {
  const qc = useQueryClient()
  const { data, error, isLoading } = useQuery({
    queryKey: ['brand-global'],
    queryFn: () => fetchJSON<BrandResp>('/api/brand/global'),
  })

  // The loaded seeds seed the local editor state; live preview overwrites tokens.
  const [primary, setPrimary] = useState('#5b21b6')
  const [accent, setAccent] = useState('')
  const [tokens, setTokens] = useState<Tokens | null>(null)
  const [exists, setExists] = useState(false)
  const [saveErr, setSaveErr] = useState('')
  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'saved'>('idle')
  const seeded = useRef(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Seed local state once from the initial GET.
  useEffect(() => {
    if (seeded.current || !data) return
    const d = data as BrandResp
    if (d.error) return
    seeded.current = true
    setTokens(d.tokens || null)
    setExists(!!d.exists)
    setPrimary((d.tokens && d.tokens.primary) || '#5b21b6')
    setAccent((d.tokens && d.tokens.accent) || '')
  }, [data])

  // previewGlobalBrand — regenerate swatches from the current seed by asking the
  // server to Generate (preview:true — never saves), so the live preview is
  // byte-identical to what Save would persist.
  const runPreview = async (p: string, a: string) => {
    if (!hexOK(p)) return // wait for a valid seed
    if (a && !hexOK(a)) return
    let d: BrandResp | null = null
    try {
      d = await apiJSON<BrandResp>('/api/brand/global', { primary: p, accent: a, preview: true })
    } catch {
      d = null
    }
    if (!d || d.error || !d.tokens) return
    setTokens(d.tokens)
  }

  // gbSeed — update a seed and re-generate the preview live (debounced 220ms).
  const gbSeed = (k: 'primary' | 'accent', v: string) => {
    const val = v.trim()
    const nextP = k === 'primary' ? val : primary
    const nextA = k === 'accent' ? val : accent
    if (k === 'primary') setPrimary(val)
    else setAccent(val)
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => runPreview(nextP, nextA), 220)
  }

  const save = useMutation({
    mutationFn: async () => {
      if (!hexOK(primary)) throw new Error('Primary must be a hex colour like #5b21b6.')
      if (accent && !hexOK(accent)) throw new Error('Accent must be a hex colour like #f97316 (or leave it blank).')
      const d = await fetchJSON<BrandResp>('/api/brand/global', { primary, accent })
      if ((d as BrandResp).error) throw new Error((d as BrandResp).error)
      return d as BrandResp
    },
    onMutate: () => {
      setSaveErr('')
      setSaveStatus('saving')
    },
    onError: (e: Error) => {
      setSaveStatus('idle')
      setSaveErr(e.message)
    },
    onSuccess: (d) => {
      if (d.tokens) setTokens(d.tokens)
      setExists(true)
      setSaveStatus('saved')
      qc.invalidateQueries({ queryKey: ['brand-global'] })
    },
  })

  if (isLoading)
    return (
      <div id="gbrandhost">
        <div className="card">
          <p className="muted" style={{ margin: 0 }}>
            loading the global brand default…
          </p>
        </div>
      </div>
    )
  if (error) return <div className="err">{String((error as Error).message || error)}</div>
  if (data && (data as BrandResp).error) return <div className="err">{(data as BrandResp).error}</div>

  const status = exists ? (
    <span className="srcpill global">
      <i />
      saved default in ~/.config/keel/brand.yaml
    </span>
  ) : (
    <span className="srcpill">
      <i />
      no default saved yet, this is a preview
    </span>
  )

  return (
    <div id="gbrandhost">
      <div className="card">
        <div
          className="row"
          style={{ justifyContent: 'space-between', alignItems: 'flex-start', gap: 12, flexWrap: 'wrap' }}
        >
          <div>
            <h3 style={{ margin: '0 0 4px' }}>House brand default</h3>
            <p className="muted" style={{ fontSize: 12.5, margin: 0, maxWidth: '60ch' }}>
              The default keel applies to <b>every</b> Tailwind or Bootstrap theme it builds. Projects inherit it unless
              they set their own override (on the project's Brand tab). Pick a seed; the full 50-950 scales, semantic
              roles, surface, radius/font and dark variant generate from it.
            </p>
          </div>
          {status}
        </div>
        <div className="seedrow" style={{ marginTop: 16 }}>
          <div className="seedfld">
            <label>Primary (seed)</label>
            <div className="swatchbox">
              <input
                type="color"
                value={hexOK(primary) ? primary : '#5b21b6'}
                onInput={(e) => gbSeed('primary', (e.target as HTMLInputElement).value)}
              />
              <input
                type="text"
                value={primary}
                placeholder="#5b21b6"
                onInput={(e) => gbSeed('primary', (e.target as HTMLInputElement).value)}
              />
            </div>
          </div>
          <div className="seedfld">
            <label>Accent (optional)</label>
            <div className="swatchbox">
              <input
                type="color"
                value={hexOK(accent) ? accent : '#f97316'}
                onInput={(e) => gbSeed('accent', (e.target as HTMLInputElement).value)}
              />
              <input
                type="text"
                value={accent}
                placeholder="auto (complementary)"
                onInput={(e) => gbSeed('accent', (e.target as HTMLInputElement).value)}
              />
            </div>
          </div>
          <div className="seedfld">
            <label>&nbsp;</label>
            <button className="btn primary" onClick={() => save.mutate()}>
              {exists ? 'Save default' : 'Set as house default'}
            </button>
          </div>
        </div>
        <div id="gb_out">
          {saveErr && (
            <div className="err" style={{ marginTop: 12 }}>
              {saveErr}
            </div>
          )}
          {!saveErr && saveStatus === 'saving' && (
            <div className="muted" style={{ marginTop: 12 }}>
              generating and saving the house default…
            </div>
          )}
          {!saveErr && saveStatus === 'saved' && (
            <div className="tag ok" style={{ marginTop: 12 }}>
              ✓ saved as the house default. Every new theme inherits this
            </div>
          )}
        </div>
        <div id="gb_panel">{tokens ? <TokensPanel t={tokens} /> : null}</div>
      </div>
    </div>
  )
}

// ============================================================================
// Settings shell — page head + Profile/Brand tab strip, sub-tab in the URL.
// ============================================================================

export default function Settings({ tab }: { tab?: string }) {
  const active = tab === 'brand' ? 'brand' : 'profile'
  const tabs = [
    { id: 'profile', label: 'Profile' },
    { id: 'brand', label: 'Brand' },
  ]
  return (
    <>
      <h1 className="page">Settings</h1>
      <p className="lede">
        Your keel defaults and the house brand, one place. Defaults pre-fill Build and the CLI reads the same profile;
        the Brand tab edits the global default every theme keel builds inherits. Per-project overrides live on the
        project's own Brand tab.
      </p>
      <div className="tabs">
        {tabs.map((t) => (
          <div
            key={t.id}
            className={'tab ' + (active === t.id ? 'on' : '')}
            {...clickable(() => navTo(t.id === 'profile' ? '#/settings' : '#/settings/' + t.id))}
          >
            {t.label}
          </div>
        ))}
      </div>
      {active === 'brand' ? <BrandTab /> : <ProfileTab />}
    </>
  )
}
