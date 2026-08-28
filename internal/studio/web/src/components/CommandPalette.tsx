import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiJSON } from '../lib/api'
import { projectsResponse, isMono, isLib, type Project, type Member } from '../lib/types'
import { navTo, projectSlug } from '../lib/router'

// The command palette, ported from the original openPalette/palCommands/palRender
// (index.html). Same #palette overlay + #palbox + #palinput + #pallist markup,
// the same item model, the same fuzzy (subsequence) filter, and the same
// keyboard behaviour: ↑/↓ move (wrapping), Enter runs the highlighted row, Esc
// closes, and a click on the backdrop (not the box) closes.

type Command = { g: string; label: string; sub?: string; run: () => void }
type Screen = { id: string; title: string; icon?: string }

// A row here is a top-level project OR a non-lib monorepo member — the original
// flattened PROJECTS + their runnable members into one list before building the
// per-project commands.
type Row = Project | Member

// fuzzy — the original's subsequence match: every char of the query appears in
// the string in order (case-insensitive). An empty query matches everything.
function fuzzy(q: string, s: string): boolean {
  q = q.toLowerCase()
  s = s.toLowerCase()
  if (!q) return true
  let i = 0
  for (const ch of s) {
    if (ch === q[i]) i++
    if (i === q.length) return true
  }
  return false
}

// Both hooks reuse the exact query keys the surfaces use, so TanStack serves them
// from cache and the palette never drifts from the sidebar/project views.
function useProjects() {
  return useQuery({
    queryKey: ['projects'],
    queryFn: () => apiJSON('/api/projects').then((d) => projectsResponse.parse(d).projects),
  })
}

function useScreens() {
  return useQuery({
    queryKey: ['plugin-screens'],
    queryFn: () =>
      apiJSON<{ screens?: Screen[] }>('/api/plugins/screens')
        .then((d) => d.screens || [])
        .catch(() => [] as Screen[]),
  })
}

// The top-level nav, mirrored from App.tsx's NAV (Settings is pinned separately,
// so it is appended after — matching how the original NAV omits it too, though
// here we surface a "Go to Settings" jump for completeness of the destinations).
const NAV = [
  { id: 'home', g: '❏', label: 'Projects', group: 'Workspace', hash: '#/' },
  { id: 'build', g: '◆', label: 'New stack', group: 'Workspace', hash: '#/build' },
  { id: 'packs', g: '◇', label: 'Packs', group: 'Extend', hash: '#/packs' },
  { id: 'plugins', g: '◈', label: 'Plugins', group: 'Extend', hash: '#/plugins' },
] as const

// buildCommands reproduces palCommands: top-level jumps, "Add an existing
// project", then one "Open project" per row plus the managed per-project actions
// (data/run/generate/auth/secrets/add/deploy) and any plugin per-project screens,
// and finally "Support this tool". Every action is pure navigation (the React
// shell drives everything through navTo), so it stays faithful and side-effect
// free.
function buildCommands(projects: Project[], screens: Screen[]): Command[] {
  const cmds: Command[] = []
  // Top-level jumps.
  NAV.forEach((a) => cmds.push({ g: a.g, label: 'Go to ' + a.label, sub: a.group, run: () => navTo(a.hash) }))
  cmds.push({
    g: '+',
    label: 'Add an existing project',
    sub: 'Projects',
    run: () => {
      navTo('#/')
      setTimeout(() => document.getElementById('addpath')?.focus(), 60)
    },
  })

  // Jump to a project, and straight into a project-scoped action. A monorepo
  // opens to its members; each non-lib member is also directly jumpable.
  const rows: Row[] = []
  projects.forEach((p) => {
    rows.push(p)
    ;(p.members || []).forEach((m) => {
      if (!isLib(m)) rows.push(m)
    })
  })
  rows.forEach((p) => {
    const slug = projectSlug(p as Project, projects)
    const to = (tab: string) => navTo('#/p/' + slug + '/' + tab)
    cmds.push({
      g: isMono(p) ? '❏' : '◉',
      label: 'Open project · ' + p.name,
      sub: isMono(p) ? 'Monorepo' : 'Project',
      run: () => to('overview'),
    })
    if (p.managed && !isMono(p)) {
      cmds.push({ g: '▤', label: 'Browse data · ' + p.name, sub: 'Data', run: () => to('data') })
      cmds.push({ g: '▷', label: 'Run tasks · ' + p.name, sub: 'Run & Logs', run: () => to('run') })
      cmds.push({ g: '✦', label: 'Generate code · ' + p.name, sub: 'Generate', run: () => to('generate') })
      cmds.push({ g: '⚿', label: 'Add auth · ' + p.name, sub: 'Generate → Stacks', run: () => to('generate') })
      cmds.push({ g: '⚷', label: 'Env & secrets · ' + p.name, sub: 'Secrets', run: () => to('secrets') })
      cmds.push({ g: '＋', label: 'Add a service · ' + p.name, sub: 'Add', run: () => to('add') })
      cmds.push({ g: '⤴', label: 'Deploy · ' + p.name, sub: 'Deploy', run: () => to('deploy') })
      screens.forEach((s) =>
        cmds.push({
          g: s.icon || '◈',
          label: s.title + ' · ' + p.name,
          sub: 'Plugin',
          run: () => navTo('#/p/' + slug + '/screen/' + encodeURIComponent(s.id)),
        }),
      )
    }
  })
  cmds.push({
    g: '♥',
    label: 'Support this tool',
    sub: 'GitHub',
    run: () => window.open('https://github.com/sponsors/coullworks', '_blank'),
  })
  return cmds
}

export default function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data: projects } = useProjects()
  const { data: screens } = useScreens()
  const [query, setQuery] = useState('')
  const [sel, setSel] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const commands = useMemo(() => buildCommands(projects || [], screens || []), [projects, screens])
  const view = useMemo(
    () => commands.filter((c) => fuzzy(query, c.label) || fuzzy(query, c.sub || '')),
    [commands, query],
  )

  // On open: reset the query, reset the highlight, and focus the input — the
  // original openPalette's i.value=""; palSel=0; …; i.focus().
  useEffect(() => {
    if (!open) return
    setQuery('')
    setSel(0)
    inputRef.current?.focus()
  }, [open])

  // Keep the highlighted row in view (palRender's scrollIntoView block:"nearest").
  useEffect(() => {
    if (!open) return
    listRef.current?.querySelector('.palrow.sel')?.scrollIntoView({ block: 'nearest' })
  }, [sel, view, open])

  const run = (i: number) => {
    const c = view[i]
    if (!c) return
    onClose()
    c.run()
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    const n = view.length
    if (e.key === 'ArrowDown') {
      if (n) setSel((s) => (s + 1) % n)
      e.preventDefault()
    } else if (e.key === 'ArrowUp') {
      if (n) setSel((s) => (s - 1 + n) % n)
      e.preventDefault()
    } else if (e.key === 'Enter') {
      run(sel)
    } else if (e.key === 'Escape') {
      onClose()
    }
  }

  return (
    <div
      id="palette"
      className={open ? 'open' : ''}
      onClick={(e) => {
        if ((e.target as HTMLElement).id === 'palette') onClose()
      }}
    >
      <div id="palbox">
        <input
          id="palinput"
          ref={inputRef}
          placeholder="Jump to a project or a project action… (↑↓ to move, ↵ to run, esc to close)"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setSel(0)
          }}
          onKeyDown={onKeyDown}
        />
        <div id="pallist" ref={listRef}>
          {view.length === 0 ? (
            <div className="palrow">no matches</div>
          ) : (
            view.map((c, i) => (
              <div key={i} className={'palrow' + (i === sel ? ' sel' : '')} onClick={() => run(i)}>
                <span className="g">{c.g}</span>
                <span>{c.label}</span>
                {c.sub ? <span className="sub">{c.sub}</span> : null}
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  )
}
