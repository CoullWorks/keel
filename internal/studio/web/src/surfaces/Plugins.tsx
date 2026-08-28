import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { apiJSON } from '../lib/api'
import { safeURL } from '../lib/url'

// The Plugins manager, ported verbatim from renderPluginsView / renderPluginsTab
// in internal/studio/index.html. A PLUGIN adds commands, wizard steps and
// per-project studio screens; this view lists them with where/state (enabled /
// disabled / not-loaded), the honest provenance, the problem (if it failed to
// load), and the actions install / enable / disable / trust / untrust /
// grant / ungrant capabilities / remove. It uses /api/plugins (GET to list,
// POST to mutate) — the same endpoints + body shapes as the original.

// A plugin row as /api/plugins returns it. Kept lax — the studio reads a stable
// subset and everything the card touches is optional on the wire.
type Plugin = {
  name: string
  version?: string
  where?: string // "built-in" | "installed" | (anything else = on PATH)
  description?: string
  problem?: string
  trustNote?: string // trusted by name but at a different path; re-trust to trust this copy
  enabled?: boolean
  trusted?: boolean
  runsCode?: boolean
  bundled?: boolean
  builtIn?: boolean
  author?: string
  homepage?: string
  capabilities?: string[]
  granted?: string[]
}

// The POST body handlePlugins accepts, matching the original pluginAction calls.
type PluginActionBody = {
  action: string
  name?: string
  source?: string
  ref?: string
  cap?: string
}

function usePlugins() {
  return useQuery({
    queryKey: ['plugins'],
    // GET /api/plugins returns {plugins:[…]} — unwrap it, or the list reads as
    // an object and renders nothing.
    queryFn: () =>
      apiJSON('/api/plugins').then((d) => ((d as { plugins?: Plugin[] })?.plugins ?? []) as Plugin[]),
  })
}

// pluginProvenance is the honest one-liner about where a plugin came from — a
// bundled built-in is a SEPARATE COULLWORKS tool that ships inside keel (its own
// author/homepage), a first-party plugin is keel's own, a PATH one is an
// external executable.
function PluginProvenance({ p }: { p: Plugin }) {
  const onPath = p.where !== 'built-in' && p.where !== 'installed'
  if (p.bundled) {
    return (
      <>
        {'separate ' + (p.author || 'COULLWORKS') + ' tool bundled with keel'}
        {p.homepage ? (
          <>
            {' · '}
            <a href={safeURL(p.homepage)} target="_blank" rel="noopener">
              {p.homepage}
            </a>
          </>
        ) : null}
      </>
    )
  }
  if (p.builtIn) return <>first-party keel plugin</>
  if (p.where === 'installed') return <>installed plugin</>
  if (onPath) return <>found on your PATH</>
  return null
}

// capLabel — the human name of a capability so a grant toggle reads as a power
// rather than a bare key. Unknown keys fall back to themselves.
function capLabel(cap: string): string {
  return (
    ({ net: 'Network access', secrets: 'Read secrets', exec: 'Run shell commands' } as Record<string, string>)[cap] ||
    cap
  )
}

// PluginCaps draws the per-capability grant/revoke toggles. Declared caps come
// from the manifest; the granted subset is the user's explicit consent. Trust is
// the prior gate — an untrusted plugin's grants are inert, so the toggles are
// shown disabled-with-reason until trusted. A plugin that declares nothing reads
// "No capabilities requested." — the correct, honest state.
function PluginCaps({ p, act }: { p: Plugin; act: (body: PluginActionBody) => void }) {
  const declared = p.capabilities || []
  if (!declared.length)
    return (
      <div className="muted" style={{ fontSize: 12, marginTop: 10 }}>
        No capabilities requested.
      </div>
    )
  const granted = new Set(p.granted || [])
  const gated = !p.trusted // caps are moot until the plugin is trusted
  return (
    <div style={{ marginTop: 10, borderTop: '1px solid var(--line)', paddingTop: 8 }}>
      <div
        className="muted"
        style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: '1px', marginBottom: 2 }}
      >
        Capabilities
      </div>
      {gated ? (
        <div className="muted" style={{ fontSize: 12, marginBottom: 2 }}>
          Trust this plugin to grant any of these.
        </div>
      ) : null}
      {declared.map((cap) => {
        const on = granted.has(cap)
        const action = on ? 'ungrant' : 'grant'
        return (
          <div className="row" key={cap} style={{ gap: 8, marginTop: 6 }}>
            <span className={'tag ' + (on && !gated ? 'ok' : 'dim')}>{on && !gated ? 'granted' : 'not granted'}</span>
            <span style={{ flex: 1, fontSize: 12.5 }}>
              <b>{capLabel(cap)}</b> <span className="muted">({cap})</span>
            </span>
            {gated ? (
              <button
                className="btn sm"
                disabled
                title={`Trust ${p.name} first. An untrusted plugin's capabilities are refused`}
              >
                Grant
              </button>
            ) : (
              <button
                className={'btn sm' + (on ? '' : ' primary')}
                onClick={() => act({ action, name: p.name, cap })}
              >
                {on ? 'Revoke' : 'Grant'}
              </button>
            )}
          </div>
        )
      })}
    </div>
  )
}

// PluginCard draws one plugin row. Enable/Disable is offered for built-in AND
// installed plugins (both persist server-side); trust and remove stay
// installed-only. A disabled plugin dims and reads "disabled".
function PluginCard({ p, act }: { p: Plugin; act: (body: PluginActionBody) => void }) {
  const onPath = p.where !== 'built-in' && p.where !== 'installed'
  const whereTag =
    p.where === 'built-in' ? (p.bundled ? 'bundled tool' : 'built-in') : p.where === 'installed' ? 'installed' : 'on PATH'
  const disabled = !p.problem && !p.enabled
  const canToggle = !onPath // built-ins and installed plugins both toggle
  return (
    <div className={'card' + (disabled ? ' dim' : '')} style={disabled ? { opacity: 0.62 } : undefined}>
      <div className="row" style={{ width: '100%' }}>
        <span style={{ fontWeight: 600 }}>{p.name}</span>
        <span className="grow"></span>
        <span className="tag">{p.version || ''}</span>
        <span className="tag">{whereTag}</span>
        <span className={'tag ' + (p.enabled ? 'ok' : 'dim')}>
          {p.problem ? 'not loaded' : p.enabled ? 'enabled' : 'disabled'}
        </span>
        {p.runsCode ? (
          <span className={'tag ' + (p.trusted ? 'ok' : 'warn')}>{p.trusted ? 'trusted' : 'untrusted'}</span>
        ) : null}
      </div>
      <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>
        <PluginProvenance p={p} />
      </div>
      {p.problem ? (
        <div className="muted" style={{ marginTop: 8 }}>
          not loaded: {p.problem}
        </div>
      ) : (
        <div className="muted" style={{ marginTop: 8 }}>
          {p.description || ''}
        </div>
      )}
      {p.trustNote ? (
        <div className="err" style={{ marginTop: 8 }}>
          {p.trustNote}
        </div>
      ) : null}
      <div className="row" style={{ gap: 8, marginTop: 10 }}>
        {canToggle ? (
          <button className="btn sm" onClick={() => act({ action: p.enabled ? 'disable' : 'enable', name: p.name })}>
            {p.enabled ? 'Disable' : 'Enable'}
          </button>
        ) : null}
        {p.where === 'installed' && p.runsCode ? (
          <button className="btn sm" onClick={() => act({ action: p.trusted ? 'untrust' : 'trust', name: p.name })}>
            {p.trusted ? 'Untrust' : 'Trust'}
          </button>
        ) : null}
        {p.where === 'installed' ? (
          <button
            className="btn sm"
            onClick={() => {
              if (!confirm('Remove ' + p.name + '?')) return
              act({ action: 'remove', name: p.name })
            }}
          >
            Remove
          </button>
        ) : null}
        {onPath ? (
          <span className="muted" style={{ fontSize: 12 }}>
            external executable, manage it where it lives
          </span>
        ) : null}
      </div>
      {p.where === 'installed' ? <PluginCaps p={p} act={act} /> : null}
    </div>
  )
}

export default function Plugins() {
  const qc = useQueryClient()
  const { data: plugins, error } = usePlugins()
  const [src, setSrc] = useState('')
  const [ref, setRef] = useState('')

  // pluginAction — POST /api/plugins with the mutation body, alert on failure,
  // then invalidate so the list (and derived nav) refresh. The original also
  // reloaded screens + plugin pages + rebuilt the nav; here invalidating
  // ['plugins'] refetches the list, and the App owns the nav/screens wiring.
  const act = async (body: PluginActionBody) => {
    try {
      const d = (await apiJSON('/api/plugins', body)) as { ok?: boolean; error?: string }
      if (d && d.ok === false) alert(d.error || 'failed')
    } catch (e) {
      alert(String(e))
    }
    await qc.invalidateQueries({ queryKey: ['plugins'] })
  }

  const install = () => {
    const source = src.trim()
    if (!source) return
    act({ action: 'add', source, ref: ref.trim() })
  }

  return (
    <>
      <h1 className="page">Plugins</h1>
      <p className="lede">
        Plugins add commands, wizard steps and per-project studio screens. Install from a folder or a git repository.
        keel reads the manifest and copies files, and never runs a plugin's code just to install it.
      </p>
      {error ? (
        <div className="err">{String((error as Error).message || error)}</div>
      ) : !plugins ? (
        <div className="muted">Loading…</div>
      ) : (
        <>
          <div className="card">
            <div className="row" style={{ gap: 8 }}>
              <input
                id="plugsrc"
                placeholder="./my-plugin  or  owner/repo"
                style={{ flex: 1 }}
                value={src}
                onChange={(e) => setSrc(e.target.value)}
              />
              <input
                id="plugref"
                placeholder="ref (optional)"
                style={{ width: 140 }}
                value={ref}
                onChange={(e) => setRef(e.target.value)}
              />
              <button className="btn" id="plugadd" onClick={install}>
                Install
              </button>
            </div>
            <div className="muted" style={{ fontSize: 12, marginTop: 8 }}>
              A folder path, a GitHub owner/repo, or any git URL.
            </div>
          </div>
          {plugins.length ? (
            plugins.map((p) => <PluginCard key={p.name} p={p} act={act} />)
          ) : (
            <div className="muted">Nothing installed yet.</div>
          )}
        </>
      )}
    </>
  )
}
