import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { apiJSON } from '../lib/api'
import { projectsResponse, isMono, pManaged, type Project } from '../lib/types'
import { navTo, projectSlug } from '../lib/router'
import { IconOrPi } from '../lib/icons'
import { clickable } from '../lib/a11y'

function useProjects() {
  return useQuery({
    queryKey: ['projects'],
    queryFn: () => apiJSON('/api/projects').then((d) => projectsResponse.parse(d).projects),
  })
}

function focusProject(p: Project, all: Project[]) {
  navTo('#/p/' + projectSlug(p, all) + '/overview')
}

function Row({ p, all }: { p: Project; all: Project[] }) {
  const dot = isMono(p) ? 'mono' : pManaged(p) ? 'keel' : ''
  const memberCount = p.members?.length ?? 0
  const sub = isMono(p)
    ? `monorepo · ${memberCount} member${memberCount === 1 ? '' : 's'}`
    : (p.framework || 'unknown') + (p.env ? ' · ' + p.env : '')
  return (
    <div className="prow" {...clickable(() => focusProject(p, all))} title={p.path}>
      <IconOrPi r={{ id: p.framework, label: p.framework }} size={26} />
      <div className="pmeta">
        <b>{p.name}</b>
        <small>{sub}</small>
      </div>
      <span className={'pdot ' + dot} title={dot === 'keel' ? 'keel-managed' : dot === 'mono' ? 'monorepo' : 'tracked'} />
    </div>
  )
}

function Rail({ projects }: { projects: Project[] }) {
  const qc = useQueryClient()
  const [path, setPath] = useState('')
  const apps = projects.filter((p) => !isMono(p))
  const monos = projects.filter(isMono)

  const add = async () => {
    const v = path.trim()
    if (!v) return
    const d = (await apiJSON('/api/projects', { path: v })) as { error?: string; added?: { path: string } }
    if (d.error) {
      alert(d.error)
      return
    }
    setPath('')
    await qc.invalidateQueries({ queryKey: ['projects'] })
  }

  return (
    <div className="rail" id="prail">
      <div className="addbox">
        <input
          id="addpath"
          placeholder="~/path/to/a/project or monorepo root"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') add()
          }}
        />
        <button className="btn sm" onClick={add}>
          Add
        </button>
      </div>
      {apps.length > 0 && (
        <>
          <div className="railcap">Projects</div>
          {apps.map((p) => (
            <Row key={p.path} p={p} all={projects} />
          ))}
        </>
      )}
      {monos.length > 0 && (
        <>
          <div className="railcap">Monorepos</div>
          {monos.map((p) => (
            <Row key={p.path} p={p} all={projects} />
          ))}
        </>
      )}
      {projects.length === 0 && (
        <div className="muted" style={{ padding: '14px 11px', fontSize: 13 }}>
          No projects yet. Add one above or build a new stack.
        </div>
      )}
    </div>
  )
}

function HomePane({ projects }: { projects: Project[] }) {
  const managedN = projects.filter(pManaged).length
  return (
    <div className="homepane" id="phomepane">
      <div className="hero">
        <h1>Your workspace</h1>
        <p>
          {projects.length} project{projects.length === 1 ? '' : 's'} tracked · {managedN} keel-managed. Click a project
          on the left to open it. Its data, tasks, generators, auth, secrets and deploys all live inside that project.
          keel auto-detects composer.json / manage.py / pyproject / package.json and monorepo workspaces.
        </p>
        <div className="quick">
          <button className="btn primary" onClick={() => navTo('#/build')}>
            ◆ Build a new stack
          </button>
          <button className="btn" onClick={() => document.getElementById('addpath')?.focus()}>
            ＋ Add an existing project
          </button>
        </div>
        <div className="tiles">
          <div className="tile" {...clickable(() => navTo('#/build'))}>
            <div className="g">◆</div>
            <b>New stack</b>
            <small>Compose a framework + env + services and build it for real.</small>
          </div>
          <div className="tile" {...clickable(() => navTo('#/packs'))}>
            <div className="g">◇</div>
            <b>Recipe packs</b>
            <small>Shareable recipe bundles that extend the build catalogue.</small>
          </div>
          <div className="tile" {...clickable(() => navTo('#/plugins'))}>
            <div className="g">◈</div>
            <b>Plugins</b>
            <small>Commands, wizard steps and per-project studio screens.</small>
          </div>
          <div className="tile" {...clickable(() => navTo('#/settings'))}>
            <div className="g">⚙</div>
            <b>Settings</b>
            <small>Your keel defaults + the house brand, one place.</small>
          </div>
        </div>
      </div>
    </div>
  )
}

export default function Home() {
  const { data: projects, error } = useProjects()
  if (error) return <div className="err">{String((error as Error).message || error)}</div>
  if (!projects) return <div className="muted">Loading…</div>
  return (
    <div className="home">
      <Rail projects={projects} />
      <HomePane projects={projects} />
    </div>
  )
}
