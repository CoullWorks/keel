import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiJSON } from './lib/api'
import { recipesResponse } from './lib/types'
import { useRoute, navTo, type Route } from './lib/router'
import { ConsoleProvider, useConsole, toastIcon } from './lib/console'
import CommandPalette from './components/CommandPalette'
import { ErrorBoundary } from './components/ErrorBoundary'
import { clickable } from './lib/a11y'
import Home from './surfaces/Home'
import Build from './surfaces/Build'
import Packs from './surfaces/Packs'
import Plugins from './surfaces/Plugins'
import Settings from './surfaces/Settings'
import ProjectDetail from './surfaces/ProjectDetail'
import PluginPage, { usePluginPages } from './surfaces/PluginPage'

// The top-level nav, ported from the original NAV array. Settings is pinned to
// the bottom (the conventional gear slot), not listed here.
const NAV = [
  { id: 'home', g: '❏', label: 'Projects', group: 'Workspace' },
  { id: 'build', g: '◆', label: 'New stack', group: 'Workspace' },
  { id: 'packs', g: '◇', label: 'Packs', group: 'Extend' },
  { id: 'plugins', g: '◈', label: 'Plugins', group: 'Extend' },
] as const

const GROUPS = ['Workspace', 'Extend'] as const

function useRecipeCount(): number | null {
  const q = useQuery({
    queryKey: ['recipes'],
    queryFn: () => apiJSON('/api/recipes').then((d) => recipesResponse.parse(d)),
  })
  return q.data ? q.data.recipes.length : null
}

function Sidebar({ route }: { route: Route }) {
  const recipeCount = useRecipeCount()
  const active = route.view
  // Plugin Pager pages fold into the Extend group as real nav destinations, one
  // entry per page — the port of renderNav's PLUGIN_PAGES.map. A failed/empty
  // fetch simply adds no entries. Shares the ['plugin-pages'] query with the
  // PluginPage surface so the two never drift.
  const { data: pluginPages } = usePluginPages()
  const activePageId = route.view === 'ppage' ? route.pageId : ''
  const item = (n: (typeof NAV)[number]) => (
    <div
      key={n.id}
      className={'navitem' + (active === n.id ? ' on' : '')}
      {...clickable(() => navTo(n.id === 'home' ? '#/' : '#/' + n.id))}
      title={n.label}
    >
      <span className="g">{n.g}</span>
      <span>{n.label}</span>
    </div>
  )
  return (
    <aside id="nav">
      <div className="brand">
        <img src="/assets/anchor.png" alt="keel" />
        <div>
          <b>
            keel<span>·</span>studio
          </b>
          <small>a console for any stack</small>
        </div>
      </div>
      <div className="navscroll" id="navscroll">
        {GROUPS.map((g) => (
          <div key={g}>
            <div className="navcap">{g}</div>
            {NAV.filter((n) => n.group === g).map(item)}
            {g === 'Extend' &&
              (pluginPages || []).map((pg) => (
                <div
                  key={pg.id}
                  className={'navitem' + (activePageId === pg.id ? ' on' : '')}
                  {...clickable(() => navTo('#/extend/' + pg.id))}
                  title={pg.title}
                >
                  <span className="g">{pg.icon || '◈'}</span>
                  <span>{pg.title}</span>
                </div>
              ))}
          </div>
        ))}
      </div>
      <div className="navpinned" id="navpinned">
        <div
          className={'navitem' + (active === 'settings' ? ' on' : '')}
          {...clickable(() => navTo('#/settings'))}
          title="Settings"
        >
          <span className="g">⚙</span>
          <span>Settings</span>
        </div>
      </div>
      <div className="navfoot">
        <div className="stat">
          <span className="dot" />
          <span id="recstat">{recipeCount == null ? '…' : `${recipeCount} recipes · zero telemetry`}</span>
        </div>
        <a className="pb" href="https://coullworks.com" target="_blank" rel="noopener" title="A COULLWORKS project">
          <span>powered by</span>
          <b>
            COULL<i>WORKS</i>
          </b>
        </a>
        <a className="support" href="https://github.com/sponsors/coullworks" target="_blank" rel="noopener">
          ♥ Support this tool
        </a>
      </div>
    </aside>
  )
}

function crumbsFor(route: Route): { label: string; here?: boolean; onClick?: () => void }[] {
  switch (route.view) {
    case 'home':
      return [{ label: 'Projects', here: true }]
    case 'build':
      return [{ label: 'New stack', here: true }]
    case 'packs':
      return [{ label: 'Packs', here: true }]
    case 'plugins':
      return [{ label: 'Plugins', here: true }]
    case 'settings':
      return [{ label: 'Settings', here: true }]
    case 'project':
      return [
        { label: 'Projects', onClick: () => navTo('#/') },
        { label: decodeURIComponent(route.slug), here: true },
      ]
    case 'ppage':
      return [{ label: decodeURIComponent(route.pageId), here: true }]
  }
}

function Header({ route, onSearch }: { route: Route; onSearch: () => void }) {
  const con = useConsole()
  const crumbs = crumbsFor(route)
  return (
    <header id="head">
      <nav className="crumbs" id="crumbs">
        {crumbs.map((c, i) => (
          <span key={i} style={{ display: 'contents' }}>
            {i > 0 && <span className="sep">›</span>}
            <span
              className={'c' + (c.here ? ' here' : '') + (c.onClick ? ' link' : '')}
              {...(c.onClick ? clickable(c.onClick) : {})}
            >
              {c.label}
            </span>
          </span>
        ))}
      </nav>
      <span className="grow" />
      <button
        className={'kbtn' + (con.open ? ' on' : '')}
        id="consoletoggle"
        onClick={() => con.setOpen(!con.open)}
        title="Show or hide the console"
      >
        ▤ Console
      </button>
      <button className="kbtn" onClick={onSearch}>
        Search &amp; jump <kbd>⌘K</kbd>
      </button>
    </header>
  )
}

function Content({ route }: { route: Route }) {
  // Each surface renders inside an error boundary so a thrown surface (or a plugin
  // page/screen it hosts) shows an inline failure instead of blanking the whole
  // studio. resetKey is the route identity, so navigating to another view — or
  // back to this one — retries rather than showing the error forever.
  const key = [
    route.view,
    'slug' in route ? route.slug : '',
    'ptab' in route ? route.ptab : '',
    'screenId' in route ? route.screenId : '',
    'pageId' in route ? route.pageId : '',
    'tab' in route ? route.tab : '',
  ]
    .filter(Boolean)
    .join(':')
  return (
    <ErrorBoundary label="This view" resetKey={key}>
      <ContentSwitch route={route} />
    </ErrorBoundary>
  )
}

function ContentSwitch({ route }: { route: Route }) {
  switch (route.view) {
    case 'home':
      return <Home />
    case 'build':
      return <Build />
    case 'packs':
      return <Packs />
    case 'plugins':
      return <Plugins />
    case 'settings':
      return <Settings tab={route.tab} />
    case 'project':
      return <ProjectDetail slug={route.slug} ptab={route.ptab} screenId={route.screenId} />
    case 'ppage':
      return <PluginPage pageId={route.pageId} />
  }
}

function Toasts() {
  const con = useConsole()
  return (
    <div id="toasts" aria-live="polite">
      {con.toasts.map((t) => (
        <div key={t.id} id={t.id} className={'toast ' + t.kind}>
          <span className="ti">{toastIcon[t.kind]}</span>
          <span className="tmsg">{t.msg}</span>
          <button className="tx" aria-label="dismiss" onClick={() => con.dismissToast(t.id)}>
            ×
          </button>
        </div>
      ))}
    </div>
  )
}

// Global chrome above the header, ported from renderUpgradeBar +
// renderPluginProblemBanner: an upgrade notice and a "N plugins failed to load"
// bar (click → Plugins) that are visible from every view. Both derive from the
// same queries the surfaces use, so they stay in step.
function ShellBanners() {
  const plugins = useQuery({
    queryKey: ['plugins'],
    queryFn: () =>
      apiJSON<{ plugins?: { name: string; problem?: string }[] }>('/api/plugins').then((d) => d.plugins || []),
  })
  const profile = useQuery({
    queryKey: ['profile'],
    queryFn: () => apiJSON<{ upgrade?: string }>('/api/profile'),
  })
  const probs = (plugins.data || []).filter((p) => p.problem)
  const upgrade = profile.data?.upgrade
  return (
    <>
      {upgrade ? <div id="upgradebar">↑ {upgrade}</div> : null}
      {probs.length > 0 ? (
        <div id="plugbar" className="plugbar" {...clickable(() => navTo('#/plugins'))}>
          ⚠ {probs.length} plugin{probs.length > 1 ? 's' : ''} failed to load (
          {probs.map((p) => p.name).join(', ')}). Click to see why
        </div>
      ) : null}
    </>
  )
}

const TERM_CLOSED_PX = 34

// The console drawer title per top-level view, ported from renderContent's
// termTitles. The project view carries the focused project's slug as a subtitle.
function consoleTitleFor(route: Route): { title: string; sub: string } {
  switch (route.view) {
    case 'build':
      return { title: 'Build console', sub: '' }
    case 'project': {
      // A member slug is a full path; show its basename (the name), as the
      // original console title did (Project · <name>).
      const s = decodeURIComponent(route.slug)
      const name = s.includes('/') ? s.split('/').filter(Boolean).pop() || s : s
      return { title: 'Project · ' + name, sub: '· ' + name }
    }
    default:
      return { title: 'Console', sub: '' }
  }
}

function AppShell() {
  const route = useRoute()
  const con = useConsole()
  const stageRef = useRef<HTMLDivElement>(null)
  const [paletteOpen, setPaletteOpen] = useState(false)

  // Keep the console title in step with the active view.
  useEffect(() => {
    const { title, sub } = consoleTitleFor(route)
    con.setTitle(title, sub)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [route.view, (route as { slug?: string }).slug])

  // Global ⌘K / Ctrl-K toggles the palette (the original's window keydown), and
  // Esc closes it — the palette's own input also handles Esc when focused, this
  // is the safety net for when focus has left the box.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen((v) => !v)
      } else if (e.key === 'Escape') {
        setPaletteOpen(false)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const startResize = (e: React.MouseEvent) => {
    e.preventDefault()
    const move = (ev: MouseEvent) => {
      const stage = stageRef.current
      if (!stage) return
      const rect = stage.getBoundingClientRect()
      const px = rect.bottom - ev.clientY
      const max = Math.round(rect.height - 120)
      con.setHeight(Math.max(TERM_CLOSED_PX, Math.min(px, max)))
      con.setOpen(px > TERM_CLOSED_PX + 8)
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  const termH = con.open ? `${con.heightPx}px` : `${TERM_CLOSED_PX}px`

  return (
    <div id="app">
      <Sidebar route={route} />
      <div id="body">
        <ShellBanners />
        <Header route={route} onSearch={() => setPaletteOpen(true)} />
        <div id="stage" ref={stageRef} style={{ ['--termH' as string]: termH }}>
          <div id="content">
            <Content route={route} />
          </div>
          <div id="dragbar" title="drag to resize the console" onMouseDown={startResize} />
          <div id="console">
            <div id="termbar">
              <span className={'tdot' + (con.live ? ' live' : '')} id="termdot" />
              <b id="termtitle">{con.title}</b>
              <span className="muted" id="termsub">
                {con.sub}
              </span>
              <span className="grow" />
              {con.live && (
                <button className="tbtn stop" id="termstop" onClick={con.stop}>
                  ■ Stop
                </button>
              )}
              <button className="tbtn" onClick={con.clear}>
                Clear
              </button>
            </div>
            <div id="term">
              {con.lines.length === 0 ? (
                <span className="muted">Output from previews, builds, tasks and DB queries streams here, live.</span>
              ) : (
                con.lines.map((l, i) => (
                  <span key={i} className={l.cls}>
                    {l.text + '\n'}
                  </span>
                ))
              )}
            </div>
          </div>
        </div>
      </div>
      <Toasts />
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
    </div>
  )
}

export default function App() {
  return (
    <ConsoleProvider>
      <AppShell />
    </ConsoleProvider>
  )
}
