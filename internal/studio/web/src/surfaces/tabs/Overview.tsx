import { useQuery, useQueryClient } from '@tanstack/react-query'
import { apiJSON } from '../../lib/api'
import { isLib, pManaged, type Project, type Member, type Backend } from '../../lib/types'
import { navTo, parseHash } from '../../lib/router'
import { Icon, iconSlug } from '../../lib/icons'
import { useConsole } from '../../lib/console'

// Overview is the React port of the studio's project OVERVIEW tab (the original
// renderOverview + loadServices/renderServices/loadFacts/renderWidgets/
// renderDBTile/renderFacts helpers in internal/studio/index.html). It is the
// data-led WIDGET dashboard: three top gauge/big-number tiles (Health, Uptime,
// Database), the running-services panel, the framework-STATISTICS grid, an
// (optional) inherited-backend card, and a thin jump strip to the other tabs.
//
// Three top-level states, ported verbatim from renderOverview:
//   - a shared library (isLib): a single calm "non-runnable" card, no dashboard.
//   - a tracked-but-not-managed project (!pManaged): the adopt/optimize card.
//   - a keel-managed project: the full dashboard.
//
// Data: the widgets + facts share ONE fetch (GET /api/overview/facts?dir=), the
// services panel its own (GET /api/services?dir=), and the DB tile its OWN async
// feed (GET /api/overview/db?dir=) so the row never blocks ~5s on a down
// database. Every tile degrades to a down/"—"/"native" state, never a blank.
// The old plugin-overview block is deliberately NOT rendered here — plugins live
// in the Plugins nav, not the overview (see overview_console_surface_test.go).

type Focus = Project | Member

// --- payload shapes (from overview.go / services.go). Kept to the fields the
// dashboard reads; the studio treats these feeds as a stable subset. ---
type Fact = { label: string; value?: string; hint?: string }
type HealthWidget = {
  servicesUp?: number
  servicesTotal?: number
  up?: boolean
  uptime?: string
  family?: string
}
type OverviewFacts = {
  framework?: string
  env?: string
  health?: HealthWidget
  git?: Fact[]
  facts?: Fact[]
  message?: string
}
type DBWidget = { up?: boolean; engine?: string; latencyMs?: number; reason?: string }
type Service = { name: string; running?: boolean; kind?: string; uptime?: string }
type ServicesResult = {
  family?: string
  up?: boolean
  services?: Service[]
  controls?: boolean
  message?: string
}

// isRootLaunch — the focused project/member runs from its workspace root; read
// off the effective-backend answer (mirrors the original isRootLaunch()).
const isRootLaunch = (be: Backend | null) => !!(be && be.rootLaunch)

// --- actions, ported from the originals. Each streams through the shared
// console (con.stream) so the keel output renders live below; con.stream
// invalidates the live surfaces on completion. ---

// --- gauge maths, ported verbatim from index.html --------------------------

// sonBand buckets a 0-100 score into good/amber/poor (sonBand).
function sonBand(n: number): 'good' | 'amber' | 'poor' {
  n = Number(n) || 0
  return n >= 90 ? 'good' : n >= 50 ? 'amber' : 'poor'
}
// sonBandColor maps that band to the studio's CSS colour var (sonBandColor).
function sonBandColor(n: number): string {
  return { good: 'var(--green)', amber: 'var(--yellow)', poor: 'var(--red)' }[sonBand(n)]
}
// dbScore maps a DB round-trip latency (ms) to a 0-100 gauge score: ≤10ms → 100,
// falling to 0 as latency climbs past ~500ms. A down/unknown DB (< 0) has no
// honest score → null (the caller shows "—"). Ported verbatim from dbScore.
function dbScore(ms: number | undefined): number | null {
  const v = Number(ms)
  if (!isFinite(v) || v < 0) return null
  if (v <= 10) return 100
  return Math.max(0, Math.round(100 - (v - 10) / 4.9))
}

// SonRing draws the band-coloured SVG ring gauge with the number centred (inline
// SVG, no chart lib), the React port of sonRing(score,size,stroke,cls). This tab
// only ever uses the "son-mnum" (mini) variant, matching renderWidgets.
function SonRing({ score, size, stroke }: { score: number; size: number; stroke: number }) {
  const s = Math.max(0, Math.min(100, Number(score) || 0))
  const r = (size - stroke) / 2
  const c = 2 * Math.PI * r
  const off = c * (1 - s / 100)
  const col = sonBandColor(s)
  return (
    <div className="son-mini">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden="true">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--line2)" strokeWidth={stroke} />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          stroke={col}
          strokeWidth={stroke}
          strokeLinecap="round"
          strokeDasharray={c.toFixed(1)}
          strokeDashoffset={off.toFixed(1)}
        />
      </svg>
      <div className="son-mnum" style={{ color: col }}>
        {s}
      </div>
    </div>
  )
}

// EmptyRing is the ring's "—"/"…" placeholder (a hollow track + a dimmed glyph),
// the JSX form of the inline <div class="son-mini">… the widgets use before a
// score resolves and for a down/unknown DB.
function EmptyRing({ glyph }: { glyph: string }) {
  return (
    <div className="son-mini">
      <svg width="64" height="64" viewBox="0 0 64 64" aria-hidden="true">
        <circle cx="32" cy="32" r="28.5" fill="none" stroke="var(--line2)" strokeWidth="7" />
      </svg>
      <div className="son-mnum" style={{ color: 'var(--dim2)' }}>
        {glyph}
      </div>
    </div>
  )
}

// iconSlugHas mirrors `iconImg(...) || …`: render the brand icon when a slug
// resolves for this framework/engine.
const hasSlug = (r: { id?: string; label?: string }) => !!iconSlug(r)

// --- Widgets row (renderWidgets + renderDBTile) ----------------------------

function HealthTile({ h }: { h: HealthWidget }) {
  const total = Number(h.servicesTotal) || 0
  const up = Number(h.servicesUp) || 0
  if (total > 0) {
    const pct = Math.round((up / total) * 100)
    return (
      <div className="ovw">
        <SonRing score={pct} size={64} stroke={7} />
        <div className="ovw-body">
          <div className="ovw-l">Health</div>
          <div className="ovw-v">
            {up}
            <span style={{ color: 'var(--dim2)', fontSize: 16 }}>/{total}</span>
          </div>
          {h.up ? (
            <span className="ovw-sub up">{up === total ? 'all running' : 'running'}</span>
          ) : (
            <span className="ovw-sub down">stopped</span>
          )}
        </div>
      </div>
    )
  }
  // A native / no-service env: an honest "native" state, not a red gauge.
  return (
    <div className="ovw">
      <div className="ovw-body">
        <div className="ovw-l">Health</div>
        <div className="ovw-v" style={{ fontSize: 19 }}>
          native
        </div>
        <div className="ovw-sub">no containers to manage</div>
      </div>
    </div>
  )
}

function UptimeTile({ uptime }: { uptime: string }) {
  return (
    <div className="ovw">
      <div className="ovw-body">
        <div className="ovw-l">Uptime</div>
        <div
          className={'ovw-v' + (uptime ? '' : ' muted')}
          style={{ fontSize: uptime ? 20 : 24 }}
          title={uptime ? 'longest-running service' : undefined}
        >
          {uptime || '—'}
        </div>
        <div className="ovw-sub">{uptime ? 'env online' : 'nothing running'}</div>
      </div>
    </div>
  )
}

// DBTile is the resolved Database widget (renderDBTile): a latency-score ring +
// engine + latency when reachable, or the calm down state when not. While the
// /api/overview/db ping is still in flight it shows the "checking…" placeholder
// (renderWidgets' #ovdb host), so the row never blocks on a down database.
function DBTile({ db, loading }: { db: DBWidget | undefined; loading: boolean }) {
  if (loading || !db) {
    return (
      <div className="ovw" id="ovdb">
        <EmptyRing glyph="…" />
        <div className="ovw-body">
          <div className="ovw-l">Database</div>
          <div className="ovw-v muted" style={{ fontSize: 15 }}>
            checking…
          </div>
        </div>
      </div>
    )
  }
  const score = db.up ? dbScore(db.latencyMs) : null
  const dbVal = db.up ? db.engine || 'database' : 'down'
  return (
    <div className="ovw" id="ovdb">
      {score === null ? <EmptyRing glyph="—" /> : <SonRing score={score} size={64} stroke={7} />}
      <div className="ovw-body">
        <div className="ovw-l">Database</div>
        <div className="ovw-v" style={{ fontSize: 19 }} title={db.up ? dbVal : undefined}>
          {dbVal}
        </div>
        {db.up ? (
          <span className="ovw-sub up">{Number(db.latencyMs) >= 0 ? db.latencyMs + ' ms' : 'reachable'}</span>
        ) : (
          <span className="ovw-sub down">not running</span>
        )}
      </div>
    </div>
  )
}

function Widgets({
  facts,
  factsLoaded,
  db,
  dbLoading,
}: {
  facts: OverviewFacts | undefined
  factsLoaded: boolean
  db: DBWidget | undefined
  dbLoading: boolean
}) {
  // widgetSkeleton: three placeholder tiles while /api/overview/facts is in
  // flight, so the top row has real shape and never flashes empty.
  if (!factsLoaded || !facts) {
    return (
      <div className="ovwidgets">
        {['Health', 'Uptime', 'Database'].map((l) => (
          <div className="ovw" key={l}>
            <div className="ovw-body">
              <div className="ovw-l">{l}</div>
              <div className="ovw-v muted" style={{ fontSize: 15 }}>
                …
              </div>
            </div>
          </div>
        ))}
      </div>
    )
  }
  const h = facts.health || {}
  // An unmanaged/unknown project has no framework facts; surface its calm
  // message once (below the widgets) rather than leaving the row silent.
  const showMsg = (!facts.facts || !facts.facts.length) && facts.message
  return (
    <div className="ovwidgets">
      <HealthTile h={h} />
      <UptimeTile uptime={h.uptime || ''} />
      <DBTile db={db} loading={dbLoading} />
      {showMsg && (
        <div className="ovw" style={{ gridColumn: '1/-1' }}>
          <div className="ovw-body">
            <div className="ovw-l">Note</div>
            <div className="ovw-sub" style={{ whiteSpace: 'normal', fontSize: 12 }}>
              {facts.message}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

// --- Services panel (renderServices) ---------------------------------------

function Services({
  p,
  be,
  data,
  isLoading,
  isError,
  error,
  onRefresh,
}: {
  p: Focus
  be: Backend | null
  data: ServicesResult | undefined
  isLoading: boolean
  isError: boolean
  error: unknown
  onRefresh: () => void
}) {
  const con = useConsole()
  // In flight: the same calm inline "Checking the environment…" the original
  // paints as the #ovservices initial state.
  if (isLoading || (!data && !isError)) {
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Services</h3>
        <p className="muted" style={{ margin: 0, fontSize: 12.5 }}>
          Checking the environment…
        </p>
      </div>
    )
  }
  // A thrown fetch degrades to a message-only result, exactly like the original's
  // catch → {message:String(e.message||e)}.
  const d: ServicesResult = isError
    ? { message: String((error as Error)?.message || error) }
    : data || {}
  const svcs = d.services || []

  // The overall-state headline: a coloured dot + a word, reusing .conn.
  let head: React.ReactNode
  if (svcs.length) {
    head = d.up ? (
      <span className="conn up">
        <span className="d" />
        environment running
      </span>
    ) : (
      <span className="conn down">
        <span className="d" />
        environment stopped
      </span>
    )
  } else {
    head = (
      <span className="conn">
        <span className="d" />
        {d.family || 'env'}
      </span>
    )
  }

  // Whole-env Start/Stop for every runtime, gated the same as the original
  // (env set, not local, not root-launch).
  const canEnv = !!p.env && p.env !== 'local' && !isRootLaunch(be)

  return (
    <div className="card">
      <div
        className="row"
        style={{
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: svcs.length ? '10px' : '2px',
        }}
      >
        <h3 style={{ margin: 0 }}>Services {head}</h3>
        <span className="row" style={{ gap: 8 }}>
          {canEnv && (
            <>
              <button
                className="btn sm"
                onClick={() => con.stream('/api/project/action', { dir: p.path, action: 'start' })}
                title="Bring the whole environment up"
              >
                ▶ Start all
              </button>
              <button
                className="btn sm"
                onClick={() => con.stream('/api/project/action', { dir: p.path, action: 'stop' })}
                title="Tear the whole environment down"
              >
                ■ Stop all
              </button>
            </>
          )}
          <button className="btn sm ghost" title="Re-check" onClick={onRefresh}>
            ↻ Refresh
          </button>
        </span>
      </div>
      {svcs.length ? (
        <div className="svcs">
          {svcs.map((s) => (
            <div className="svc" key={s.name}>
              {s.running ? (
                <span className="conn up">
                  <span className="d" />
                </span>
              ) : (
                <span className="conn down">
                  <span className="d" />
                </span>
              )}
              <b className="svc-name">{s.name}</b>
              <span className={'svc-state ' + (s.running ? 'up' : 'down')}>{s.running ? 'up' : 'down'}</span>
              {s.kind && (
                <span className="muted" style={{ fontSize: 11.5 }}>
                  {s.kind}
                </span>
              )}
              <span className="grow" />
              {s.running && s.uptime && (
                <span className="svc-up">
                  up <b>{s.uptime}</b>
                </span>
              )}
              {d.controls && (
                <span className="svc-acts">
                  <button
                    className="btn sm"
                    title={'Start ' + s.name}
                    onClick={() => con.stream('/api/service/action', { dir: p.path, service: s.name, action: 'start' })}
                  >
                    ▶
                  </button>
                  <button
                    className="btn sm"
                    title={'Stop ' + s.name}
                    onClick={() => con.stream('/api/service/action', { dir: p.path, service: s.name, action: 'stop' })}
                  >
                    ■
                  </button>
                  <button
                    className="btn sm"
                    title={'Restart ' + s.name}
                    onClick={() => con.stream('/api/service/action', { dir: p.path, service: s.name, action: 'restart' })}
                  >
                    ↻
                  </button>
                </span>
              )}
            </div>
          ))}
        </div>
      ) : (
        <p className="muted" style={{ margin: '10px 0 0', fontSize: 12.5 }}>
          {d.message || 'No services to show.'}
        </p>
      )}
    </div>
  )
}

// --- Statistics grid (renderFacts) -----------------------------------------

function Facts({
  facts,
  factsLoaded,
  onRefresh,
}: {
  facts: OverviewFacts | undefined
  factsLoaded: boolean
  onRefresh: () => void
}) {
  // The initial "Reading framework statistics…" state the original paints into
  // #ovfacts before the facts fetch lands.
  if (!factsLoaded || !facts) {
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Statistics</h3>
        <p className="muted" style={{ margin: 0, fontSize: 12.5 }}>
          Reading framework statistics…
        </p>
      </div>
    )
  }
  const d = facts
  // Identity + git lead the grid; then the per-framework statistics.
  const base: Fact[] = [
    { label: 'Framework', value: d.framework || '', hint: "the project's framework" },
    { label: 'Environment', value: d.env || '', hint: 'the keel env recipe' },
  ]
  const all = base.concat(d.git || []).concat(d.facts || [])
  const showNote = (!d.facts || !d.facts.length) && d.message
  return (
    <div className="card">
      <div className="row" style={{ justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
        <h3 style={{ margin: 0 }}>Statistics</h3>
        <button className="btn sm ghost" title="Re-read statistics" onClick={onRefresh}>
          ↻ Refresh
        </button>
      </div>
      <div className="ovstats statgrid">
        {all.map((f, i) => {
          const v = f.value || ''
          return (
            <div className="ovstat" key={f.label + i} title={f.hint || undefined}>
              <div className={'ovstat-v' + (v ? '' : ' dash')} title={v || undefined}>
                {v || '—'}
              </div>
              <div className="ovstat-l">{f.label}</div>
              {f.hint && <div className="ovstat-s">{f.hint}</div>}
            </div>
          )
        })}
      </div>
      {showNote && (
        <p className="muted" style={{ margin: '10px 0 0', fontSize: 12.5 }}>
          {d.message}
        </p>
      )}
    </div>
  )
}

// currentSlug reads the focused project's slug off the live hash route so the
// jump strip can navigate to the sibling tabs the same way the tab bar does
// (setPTab in the original). The overview only renders inside #/p/<slug>/…, so
// the route always carries a slug here.
function currentSlug(): string {
  const r = parseHash(location.hash)
  return r.view === 'project' ? r.slug : ''
}

export default function OverviewTab({ p, be }: { p: Project | Member; be: Backend | null }) {
  const qc = useQueryClient()
  const con = useConsole()

  // A shared library: a single calm card, no dashboard (renderOverview isLib).
  if (isLib(p)) {
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Shared library</h3>
        <p className="muted" style={{ fontSize: 13, margin: 0 }}>
          This is a shared workspace package with no framework of its own. Other apps depend on it. It is non-runnable, so
          keel offers no run/data/deploy actions for it.
        </p>
      </div>
    )
  }

  // Tracked but not keel-managed: the adopt / optimize card (renderOverview
  // !pManaged). adopt writes a manifest then refreshes the project list so the
  // managed action set unlocks — the same effect as the original adopt(dir).
  if (!pManaged(p)) {
    const adopt = async () => {
      await con.stream('/api/exec', { dir: p.path, args: ['adopt'] })
      await qc.invalidateQueries({ queryKey: ['projects'] })
    }
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Not keel-managed yet</h3>
        <p className="muted" style={{ fontSize: 13, margin: '0 0 12px' }}>
          keel has tracked this {p.framework || 'project'} but it has no keel manifest, so the managed actions (Data, Run,
          Generate, Auth, Secrets, Add-service, Deploy) are locked. Adopt it to write a manifest and unlock them. Your
          files are never overwritten.
        </p>
        <div className="row" style={{ gap: 8 }}>
          {p.framework && p.framework !== 'unknown' && (
            <button className="btn primary" onClick={adopt}>
              Make editable (adopt)
            </button>
          )}
          <button className="btn" onClick={() => con.stream('/api/exec', { dir: p.path, args: ['optimize'] })}>
            Optimize (read-only scan)
          </button>
        </div>
      </div>
    )
  }

  // Managed: the full data-led widget dashboard. The facts feed (widgets + the
  // stat grid) and the services feed are keyed by the project path so switching
  // focus refetches; the DB tile has its own async feed so it never blocks the
  // rest of the dashboard (~5s on a down database).
  return <ManagedDashboard p={p} be={be} />
}

function ManagedDashboard({ p, be }: { p: Focus; be: Backend | null }) {
  const con = useConsole()
  const dir = p.path
  const slug = currentSlug()

  const factsQ = useQuery({
    queryKey: ['overview-facts', dir],
    queryFn: () => apiJSON<OverviewFacts>('/api/overview/facts?dir=' + encodeURIComponent(dir)),
    retry: false,
  })
  const dbQ = useQuery({
    queryKey: ['overview-db', dir],
    queryFn: () => apiJSON<DBWidget>('/api/overview/db?dir=' + encodeURIComponent(dir)),
    retry: false,
  })
  const servicesQ = useQuery({
    queryKey: ['overview-services', dir],
    queryFn: () => apiJSON<ServicesResult>('/api/services?dir=' + encodeURIComponent(dir)),
    retry: false,
  })

  // A thrown facts fetch degrades to a message-only payload — the same
  // {message:String(e)} the original loadFacts falls back to — so the widgets
  // and grid render their calm note rather than a blank.
  const facts: OverviewFacts | undefined = factsQ.isError
    ? { facts: [], message: String((factsQ.error as Error)?.message || factsQ.error) }
    : factsQ.data
  const factsLoaded = factsQ.isSuccess || factsQ.isError
  // A thrown DB fetch degrades to the calm down state (loadDBWidget's catch).
  const db: DBWidget | undefined = dbQ.isError ? { up: false, latencyMs: -1 } : dbQ.data
  const dbLoading = dbQ.isLoading

  // The inherited/own backend card (beCard) — only when a backend engine is known.
  const beCard =
    be && be.engine ? (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>{be.inherited ? 'Shared backend (inherited)' : 'Backend'}</h3>
        <div className="row" style={{ gap: 10 }}>
          {hasSlug({ id: be.provider || be.engine, label: be.engine }) && (
            <Icon r={{ id: be.provider || be.engine, label: be.engine }} size={22} />
          )}
          <div>
            <b>
              {be.engine}
              {be.provider ? ' · ' + be.provider : ''}
            </b>
            {be.source && (
              <div className="muted" style={{ fontSize: 12 }}>
                {be.source}
              </div>
            )}
          </div>
        </div>
      </div>
    ) : null

  return (
    <>
      <Widgets facts={facts} factsLoaded={factsLoaded} db={db} dbLoading={dbLoading} />
      <div>
        <Services
          p={p}
          be={be}
          data={servicesQ.data}
          isLoading={servicesQ.isLoading}
          isError={servicesQ.isError}
          error={servicesQ.error}
          onRefresh={() => servicesQ.refetch()}
        />
      </div>
      <div>
        <Facts facts={facts} factsLoaded={factsLoaded} onRefresh={() => factsQ.refetch()} />
      </div>
      {beCard}
      {/* A thin jump strip to the other tabs (NOT a Quick-actions card) — ovjump. */}
      <div className="ovjump">
        <button className="btn sm" onClick={() => navTo('#/p/' + slug + '/data')}>
          ▤ Data
        </button>
        <button className="btn sm" onClick={() => navTo('#/p/' + slug + '/run')}>
          ▷ Run
        </button>
        <button className="btn sm" onClick={() => navTo('#/p/' + slug + '/generate')}>
          ✦ Generate
        </button>
        <button className="btn sm" onClick={() => navTo('#/p/' + slug + '/add')}>
          ＋ Services
        </button>
        <button className="btn sm" onClick={() => navTo('#/p/' + slug + '/secrets')}>
          ⚷ Secrets
        </button>
        <button className="btn sm" onClick={() => navTo('#/p/' + slug + '/deploy')}>
          ⤴ Deploy
        </button>
        <button className="btn sm" onClick={() => con.stream('/api/exec', { dir, args: ['update'] })}>
          ↑ Update
        </button>
      </div>
    </>
  )
}
