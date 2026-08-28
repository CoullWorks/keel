import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, apiJSON } from '../lib/api'
import { projectsResponse, isMono, isLib, pManaged, type Project, type Member, type Backend } from '../lib/types'
import { navTo, projectSlug, slugToPath } from '../lib/router'
import { clickable } from '../lib/a11y'
import { Icon, iconSlug } from '../lib/icons'
import { useConsole } from '../lib/console'
import { ScreenView, type Section } from '../lib/pluginview'
import { PluginComponent } from '../lib/pluginmount'
import OverviewTab from './tabs/Overview'
import DataTab from './tabs/Data'
import SecretsTab from './tabs/Secrets'
import BrandTab from './tabs/Brand'
import ManageTab from './tabs/Manage'
import DeployTab from './tabs/Deploy'
import RunTab from './tabs/Run'
import GenerateTab from './tabs/Generate'

// ProjectDetail is the React port of the studio's project-first detail view
// (renderProject/renderProjHeader/renderProjTabs/renderProjTabBody/monorepoBody).
// A persistent header always says what you are acting on, then either a
// monorepo's members list or the tab strip + the active tab body.

// The effective (possibly inherited) backend the header + tab gating read, as
// /api/member/backend returns it. Kept lax — the studio reads a stable subset.
// A row we act on is a top-level project or a monorepo member. Both carry the
// stable subset the header reads; a member has no members of its own.
type Focus = Project | Member

// PTABS — the tab set for a focused project, gating managed tabs on a keel
// manifest, ported verbatim from the original const PTABS.
type Tab = { id: string; g: string; label: string; need?: string; screen?: unknown }
const PTABS: Tab[] = [
  { id: 'overview', g: '◉', label: 'Overview' },
  { id: 'data', g: '▤', label: 'Data', need: 'managed' },
  { id: 'run', g: '▷', label: 'Run & Logs', need: 'managed' },
  { id: 'generate', g: '✦', label: 'Generate', need: 'managed' },
  { id: 'brand', g: '◑', label: 'Brand', need: 'managed' },
  { id: 'secrets', g: '⚷', label: 'Env & Secrets', need: 'managed' },
  { id: 'add', g: '＋', label: 'Manage services', need: 'managed' },
  { id: 'deploy', g: '⤴', label: 'Deploy', need: 'managed' },
]

// Plugin per-project Screener screens are appended to the tab strip. The list is
// framework-wide — the screens plugins registered (GET /api/plugins/screens) —
// and each becomes a per-project tab; one project's Render is fetched only when
// its tab is open (POST /api/plugins/screen). Sonar's "Visibility" is one.
//
// A screen carries a component URL + its owning plugin when it is the component
// tier (a built ES module the studio mounts, the per-project mirror of a page's
// component); those are absent for a data/render screen, which draws a View.
type Screen = { id: string; title: string; icon?: string; plugin?: string; component?: string }

function useProjects() {
  return useQuery({
    queryKey: ['projects'],
    queryFn: () => apiJSON('/api/projects').then((d) => projectsResponse.parse(d).projects),
  })
}

// useScreens fetches the registered plugin screens (['plugin-screens']). It takes
// no params — the list is the plugins' registration, not per-project — matching
// the original loadScreens (GET /api/plugins/screens → {screens}). A failed fetch
// degrades to [] (loadScreens' catch), so the tab strip simply gains no screens.
function useScreens() {
  return useQuery({
    queryKey: ['plugin-screens'],
    queryFn: () =>
      apiJSON<{ screens?: Screen[] }>('/api/plugins/screens')
        .then((d) => d.screens || [])
        .catch(() => [] as Screen[]),
  })
}

// findFocus resolves a path to a top-level project or a monorepo member,
// mirroring the original findProject.
function findFocus(path: string, projects: Project[]): { focus: Focus; parent: Project | null } | null {
  for (const p of projects) {
    if (p.path === path) return { focus: p, parent: null }
    for (const m of p.members || []) if (m.path === path) return { focus: m, parent: p }
  }
  return null
}

// isRootLaunch reports whether the focused project/member runs from its
// workspace root (pnpm/turbo dev) — read off the effective-backend answer.
const isRootLaunch = (be: Backend | null) => !!(be && be.rootLaunch)

// projTabsFor is the tab set for the focused project: managed tabs gated on a
// keel manifest, deploy dropped for a root-launch member, and each plugin
// screen appended as its own tab.
function projTabsFor(p: Focus, be: Backend | null, screens: Screen[]): Tab[] {
  const managed = pManaged(p) && !isLib(p)
  let tabs = PTABS.filter((t) => t.need !== 'managed' || managed)
  if (isRootLaunch(be)) tabs = tabs.filter((t) => t.id !== 'deploy')
  if (managed) screens.forEach((s) => tabs.push({ id: 'screen:' + s.id, g: s.icon || '◈', label: s.title, screen: s }))
  return tabs
}

// --- actions: POST/DELETE their exact endpoints, ported from the originals.
// The env Start/Stop stream through the shared console (con.stream); the rest
// keep their fire-and-forget contract (open-in-editor never streams). ---

// projAction — POST /api/project/action {dir, action} (open-in-editor only; the
// env Start/Stop stream via con.stream instead).
function projAction(dir: string, action: string) {
  return apiJSON('/api/project/action', { dir, action }).catch(() => {})
}

// projExec — POST /api/exec {dir, args} (the adopt button).
function projExec(dir: string, args: string[]) {
  return apiJSON('/api/exec', { dir, args }).catch(() => {})
}

function ProjHeader({ p, projects, be }: { p: Focus; projects: Project[]; be: Backend | null }) {
  const qc = useQueryClient()
  const con = useConsole()

  // Live connectivity dot for a managed, non-monorepo project — the same
  // /api/db/ping the Data tab gates on (pingConn).
  const conn = pManaged(p) && !isMono(p)
  const pingQ = useQuery({
    queryKey: ['db-ping', p.path],
    queryFn: () => apiJSON<{ reachable?: boolean }>('/api/db/ping?dir=' + encodeURIComponent(p.path)),
    enabled: conn,
    retry: false,
  })

  // adopt — write a keel manifest, then refresh the list so the managed action
  // set unlocks (mirrors the original adopt(dir)).
  const adopt = async (dir: string) => {
    await projExec(dir, ['adopt'])
    await qc.invalidateQueries({ queryKey: ['projects'] })
  }

  // removeProject — confirm, DELETE /api/projects {path}, refresh, and if the
  // focused project is gone, go home (mirrors the original removeProject).
  const removeProject = async (path: string) => {
    if (!confirm('Untrack this project? Its files on disk are kept.')) return
    await api('/api/projects', { method: 'DELETE', body: JSON.stringify({ path }) }).catch(() => {})
    await qc.invalidateQueries({ queryKey: ['projects'] })
    const stillThere = findFocus(path, qc.getQueryData<Project[]>(['projects']) || projects)
    if (!stillThere) navTo('#/')
  }

  // A root-launch member does not own an env to bring up.
  const canStop = !!p.env && p.env !== 'local' && !isRootLaunch(be)

  const chips: React.ReactNode[] = []
  chips.push(
    <span key="fw" className="tag">
      {isMono(p) ? 'monorepo' : isLib(p) ? 'library' : p.framework || 'unknown'}
    </span>,
  )
  if (p.env)
    chips.push(
      <span key="env" className="tag">
        {p.env}
      </span>,
    )
  chips.push(
    pManaged(p) ? (
      <span key="mng" className="tag ok">
        keel-managed
      </span>
    ) : (
      <span key="mng" className="tag dim">
        tracked
      </span>
    ),
  )
  if (be && be.inherited && be.engine)
    chips.push(
      <span key="shared" className="tag warn" title="Inherited from the monorepo root">
        shared {be.engine}
        {be.provider ? ' · ' + be.provider : ''}
      </span>,
    )
  if (isRootLaunch(be))
    chips.push(
      <span key="root" className="tag warn" title="Launched from the workspace root, no per-member env">
        root-launch · {be?.launchManager || 'workspace'}
      </span>,
    )

  const acts: React.ReactNode[] = []
  acts.push(
    <button key="back" className="btn sm ghost" onClick={() => navTo('#/')} title="Back to all projects">
      ← Projects
    </button>,
  )
  if (!isLib(p)) {
    acts.push(
      <button key="open" className="btn sm" onClick={() => projAction(p.path, 'open')} title="Open in your editor">
        ✎ Open
      </button>,
    )
    if (canStop) {
      acts.push(
        <button
          key="start"
          className="btn sm"
          onClick={() => con.stream('/api/project/action', { dir: p.path, action: 'start' })}
          title="Bring the dev environment up"
        >
          ▶ Start
        </button>,
      )
      acts.push(
        <button
          key="stop"
          className="btn sm"
          onClick={() => con.stream('/api/project/action', { dir: p.path, action: 'stop' })}
          title="Tear the dev environment down"
        >
          ■ Stop
        </button>,
      )
    }
  }
  if (!pManaged(p) && !isMono(p) && !isLib(p) && p.framework && p.framework !== 'unknown') {
    acts.push(
      <button
        key="adopt"
        className="btn sm primary"
        onClick={() => adopt(p.path)}
        title="Adopt: write a keel manifest so the full action set unlocks"
      >
        Make editable
      </button>,
    )
  }

  // Connectivity dot: checking while the ping is in flight, then up/down.
  let connEl: React.ReactNode = null
  if (conn) {
    if (pingQ.isFetching || (pingQ.data === undefined && !pingQ.isError)) {
      connEl = (
        <span className="conn checking" id="pconn">
          <span className="d" />
          checking…
        </span>
      )
    } else if (pingQ.data?.reachable) {
      connEl = (
        <span className="conn up" id="pconn">
          <span className="d" />
          database reachable
        </span>
      )
    } else {
      connEl = (
        <span className="conn down" id="pconn">
          <span className="d" />
          database not running
        </span>
      )
    }
  }

  return (
    <div className="phead">
      {iconSlugHas(p) ? <Icon r={{ id: p.framework, label: p.framework }} size={40} /> : <span className="pi" />}
      <div className="ptit">
        <b>{p.name}</b>
        <div className="ppath">{p.path}</div>
        <div className="pchips">
          {chips}
          {connEl}
        </div>
      </div>
      <div className="pacts">
        {acts}
        <button className="btn sm ghost" title="Untrack (files kept)" onClick={() => removeProject(p.path)}>
          ✕
        </button>
      </div>
    </div>
  )
}

// iconSlugHas mirrors `iconImg(...) || '<span class="pi">'`: render the brand
// icon when a slug resolves for this framework, else the neutral placeholder.
function iconSlugHas(p: Focus): boolean {
  return !!iconSlug({ id: p.framework, label: p.framework })
}

function MemberRow({ m, projects }: { m: Member; projects: Project[] }) {
  const lib = isLib(m)
  const open = () => {
    if (lib) return
    // focusProject a member = navigate to its slug/overview.
    navTo('#/p/' + projectSlug(m as Project, projects) + '/overview')
  }
  const badge = lib ? (
    <span className="tag dim">library · non-runnable</span>
  ) : m.managed ? (
    <span className="tag ok">keel</span>
  ) : (
    <span className="tag dim">{m.framework || 'unknown'}</span>
  )
  return (
    <div className={'member' + (lib ? ' lib' : '')} {...(lib ? {} : clickable(open))}>
      {iconSlugHas(m) ? <Icon r={{ id: m.framework, label: m.framework }} size={26} /> : <span className="mi" />}
      <div className="mmeta">
        <b>{m.name}</b> <span className="muted" style={{ fontSize: 12 }}>{m.framework}</span>
      </div>
      {badge}
      {!lib && (
        <button
          className="btn sm"
          onClick={(e) => {
            e.stopPropagation()
            open()
          }}
        >
          Open →
        </button>
      )}
    </div>
  )
}

function MonorepoBody({ p, be, projects }: { p: Project; be: Backend | null; projects: Project[] }) {
  const members = p.members || []
  // A launchCommand rides along on the project as a passthrough field.
  const launchCommand = (p as Project & { launchCommand?: string }).launchCommand

  return (
    <div>
      {launchCommand && (
        <div className="backend" style={{ marginBottom: 14 }}>
          <Icon r={{ id: 'turborepo', label: 'workspace' }} size={22} />
          <div style={{ flex: 1 }}>
            <div className="bl">This workspace launches from the root. Every member comes up together</div>
            <b>
              <code>{launchCommand}</code>
            </b>
          </div>
          <button
            className="btn primary sm"
            onClick={() => apiJSON('/api/run', { dir: p.path, task: 'dev' }).catch(() => {})}
          >
            ▷ Run
          </button>
        </div>
      )}
      {be && be.engine ? (
        <div className="backend">
          <Icon r={{ id: be.provider || be.engine, label: be.engine }} size={22} />
          <div style={{ flex: 1 }}>
            <div className="bl">Shared backend, all members inherit this</div>
            <b>
              {be.engine}
              {be.provider ? ' · ' + be.provider : ''}
            </b>
            {be.source && (
              <span className="muted" style={{ fontSize: 12 }}>
                {' '}
                · {be.source}
              </span>
            )}
            {be.schema && (
              <span className="muted" style={{ fontSize: 12 }}>
                {' '}
                · schema {be.schema}
              </span>
            )}
          </div>
        </div>
      ) : (
        <div className="card" style={{ marginBottom: 14 }}>
          <p className="muted" style={{ margin: 0, fontSize: 12.5 }}>
            No shared backend recorded on this monorepo root yet. Adopt the root (Make editable) so members can inherit
            one, or each member resolves its own.
          </p>
        </div>
      )}
      <h2 style={{ marginBottom: 12 }}>Members</h2>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
        {members.length ? (
          members.map((m) => <MemberRow key={m.path} m={m} projects={projects} />)
        ) : (
          <div className="muted" style={{ fontSize: 12.5 }}>
            no recognised members in the workspaces
          </div>
        )}
      </div>
    </div>
  )
}

// ScreenTab renders one plugin per-project screen: POST /api/plugins/screen
// {id, dir} → a View the studio draws (ScreenView). The port of renderScreen's
// DATA path — its lede ("<title> for <name>."), loading, error, and the "returned
// nothing" empty state (kept verbatim). The ai-core interactive OptionStepper
// form is a separate concern and is not ported here.
// ScreenTab renders one plugin per-project screen. A component screen (one that
// ships a built ES module) is MOUNTED, threading the project dir so its keel.call
// runs the action against this project — the per-project mirror of a component
// page. Every other screen takes the DATA path (ScreenDataTab), unchanged.
function ScreenTab({ s, p }: { s: Screen; p: Focus }) {
  if (s.component) {
    return (
      <>
        <p className="lede" style={{ marginTop: 0 }}>
          {s.title} for <b>{p.name}</b>.
        </p>
        <PluginComponent url={s.component} plugin={s.plugin || ''} dir={p.path} />
      </>
    )
  }
  return <ScreenDataTab s={s} p={p} />
}

// ScreenDataTab is the DATA path: POST /api/plugins/screen {id, dir} → a View the
// studio draws (ScreenView), with its lede, loading, error, and empty state kept
// verbatim. Split out of ScreenTab so the useQuery hook stays unconditional.
function ScreenDataTab({ s, p }: { s: Screen; p: Focus }) {
  const q = useQuery({
    queryKey: ['plugin-screen', s.id, p.path],
    queryFn: () =>
      apiJSON<{ sections?: Section[]; error?: string }>('/api/plugins/screen', { id: s.id, dir: p.path }),
    retry: false,
  })

  const host = (inner: React.ReactNode) => (
    <>
      <p className="lede" style={{ marginTop: 0 }}>
        {s.title} for <b>{p.name}</b>.
      </p>
      <div id="screenhost">{inner}</div>
    </>
  )

  if (q.isLoading || (!q.data && !q.isError)) {
    return host(<span className="muted">loading…</span>)
  }
  const err = q.isError ? String((q.error as Error)?.message || q.error) : q.data?.error
  if (err) {
    return host(<div className="err">{err}</div>)
  }
  const sections = q.data?.sections || []
  if (!sections.length) {
    return host(
      <div className="muted">This screen returned nothing for this project. Is its environment running?</div>,
    )
  }
  return host(<ScreenView sections={sections} />)
}

// TabBody dispatches the active tab. A screen: tab renders the plugin screen;
// every built-in tab has its ported React surface.
function TabBody({
  ptab,
  p,
  be,
  screens,
}: {
  ptab: string
  p: Focus
  be: Backend | null
  screens: Screen[]
}) {
  if (ptab.startsWith('screen:')) {
    const s = screens.find((x) => 'screen:' + x.id === ptab)
    if (!s) return <div className="muted">Pick a plugin screen tab.</div>
    return <ScreenTab s={s} p={p} />
  }
  switch (ptab) {
    case 'overview':
      return <OverviewTab p={p} be={be} />
    case 'data':
      return <DataTab p={p} be={be} />
    case 'run':
      return <RunTab p={p} be={be} />
    case 'generate':
      return <GenerateTab p={p} be={be} />
    case 'brand':
      return <BrandTab p={p} be={be} />
    case 'secrets':
      return <SecretsTab p={p} be={be} />
    case 'add':
      return <ManageTab p={p} be={be} />
    case 'deploy':
      return <DeployTab p={p} be={be} />
    default:
      // The original renderProjTabBody falls back to the overview for an
      // unknown tab; every built-in tab above has its ported surface.
      return <OverviewTab p={p} be={be} />
  }
}

// tabHref serialises a tab to its deep link, matching the original serialise():
// a plugin screen tab (id "screen:<id>") uses the /screen/<id> form the router
// parses; every other tab is /p/<slug>/<id>.
function tabHref(slug: string, tabId: string): string {
  if (tabId.startsWith('screen:')) return '#/p/' + slug + '/screen/' + encodeURIComponent(tabId.slice('screen:'.length))
  return '#/p/' + slug + '/' + tabId
}

export default function ProjectDetail({ slug, ptab }: { slug: string; ptab: string; screenId?: string }) {
  const { data: projects, error } = useProjects()
  const { data: screens } = useScreens()

  // Resolve the effective backend for the header's shared-backend chip +
  // isRootLaunch. Tolerates {error}/null. Keyed on the resolved path so it
  // refetches when focus moves.
  const path = projects ? slugToPath(slug, projects) : ''
  const backendQ = useQuery({
    queryKey: ['member-backend', path],
    queryFn: () => apiJSON<Backend>('/api/member/backend?dir=' + encodeURIComponent(path)),
    enabled: !!path && !!projects,
    retry: false,
  })

  if (error) return <div className="err">{String((error as Error).message || error)}</div>
  if (!projects) return <div className="muted">Loading…</div>

  const hit = findFocus(path, projects)
  if (!hit) {
    navTo('#/')
    return null
  }
  const { focus: p } = hit
  const be: Backend | null = backendQ.data && !backendQ.data.error ? backendQ.data : null

  // Gate the tab set the same way the original renderProjTabs does, and fall
  // back to overview when the requested tab is not available for this project.
  const tabs = projTabsFor(p, be, screens || [])
  const activePtab = tabs.some((t) => t.id === ptab) ? ptab : 'overview'

  return (
    <>
      <div id="phead">
        <ProjHeader p={p} projects={projects} be={be} />
      </div>
      <div id="pdetail">
        {isMono(p) ? (
          <MonorepoBody p={p as Project} be={be} projects={projects} />
        ) : (
          <>
            <div className="ptabs" id="ptabs">
              {tabs.map((t) => (
                <div
                  key={t.id}
                  className={'ptab ' + (activePtab === t.id ? 'on' : '')}
                  data-tab={t.id}
                  {...clickable(() => navTo(tabHref(projectSlug(p as Project, projects), t.id)))}
                >
                  <span className="tg">{t.g}</span>
                  {t.label}
                </div>
              ))}
            </div>
            <div id="tabbody">
              <TabBody ptab={activePtab} p={p} be={be} screens={screens || []} />
            </div>
          </>
        )}
      </div>
    </>
  )
}
