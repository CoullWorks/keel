import { useQuery, useQueryClient } from '@tanstack/react-query'
import { fetchJSON } from '../../lib/api'
import type { Project, Member, Backend } from '../../lib/types'
import { Icon } from '../../lib/icons'
import { useConsole } from '../../lib/console'

// Manage is the React port of the studio's per-project MANAGE SERVICES tab (the
// original renderAddTab + installedCard/addableCard/addRecipe/removeRecipe in
// internal/studio/index.html). It shows the recipes already installed in the
// project (each removable except the framework/env, which define it) and a
// picker of addable recipes grouped by kind. Adding or removing a service runs
// keel add/keel remove.
//
// Both reads run in parallel and are keyed by the project path so switching
// focus refetches: what is INSTALLED (to list + remove) and what is ADDABLE (to
// add). Either failing degrades to a clear message, never a blank; both failing
// shows the shared error, mirroring the original.
//
// Actions: add/remove stream through the shared console (con.stream('/api/exec',
// {dir, args:[...]})) so the keel add/remove output renders live below. The
// stream invalidates queries on completion; onChanged also invalidates
// ['installed',dir] + ['addable',dir] so the row moves between the two cards.

// KIND_LABEL — the addable picker's group headings, ported verbatim.
const KIND_LABEL: Record<string, string> = {
  service: 'Services',
  db: 'Databases',
  frontend: 'UI kits & frontends',
  addon: 'Addons',
  extra: 'Extras',
  config: 'Config',
  generator: 'Generators',
}

// --- payload shapes (from installed.go / addable.go). Kept to the fields the
// tab reads; the studio treats these feeds as a stable subset. ---
type Recipe = {
  id: string
  label?: string
  kind?: string
  source?: string
  removable?: boolean
}
type InstalledResult = { framework?: string; installed?: Recipe[]; error?: string }
type AddableResult = { framework?: string; addable?: Recipe[]; error?: string }

// InstalledCard lists the recipes already in the manifest, each with a Remove
// (except the framework/env, which define the project — shown, not removable).
function InstalledCard({ d, dir, onChanged }: { d: InstalledResult; dir: string; onChanged: () => void }) {
  const con = useConsole()
  const list = d.installed || []
  // removeRecipe — confirm, then stream keel remove (the studio exec path cannot
  // answer the TTY confirm, so --yes is required). Skips when the user cancels.
  const removeRecipe = async (id: string) => {
    if (!confirm("Remove " + id + " from this project's manifest? Files on disk are kept.")) return
    await con.stream('/api/exec', { dir, args: ['remove', id, '--yes'] })
    onChanged()
  }
  return (
    <div className="card">
      <h3 style={{ marginTop: 0 }}>Installed in {d.framework || 'this project'}</h3>
      <p className="muted" style={{ fontSize: 12.5, margin: '0 0 8px' }}>
        Remove runs <code>keel remove &lt;recipe&gt;</code>. It stops managing the recipe in the manifest; files an
        installer wrote are left for you to remove. Output streams below.
      </p>
      {list.length ? (
        <div className="svcs">
          {list.map((r) => (
            <div className="svc" key={r.id}>
              <b className="svc-name">{r.label || r.id}</b>
              {r.kind && (
                <span className="svc-state" style={{ borderColor: 'var(--line2)', color: 'var(--dim)' }}>
                  {r.kind}
                </span>
              )}
              {r.source && r.source !== 'builtin' && (
                <span className="muted" style={{ fontSize: 11.5 }}>
                  {r.source}
                </span>
              )}
              <span className="grow" />
              {r.removable ? (
                <button
                  className="btn sm ghost"
                  title={'Remove ' + r.id + ' from the manifest (files are kept)'}
                  onClick={() => removeRecipe(r.id)}
                >
                  Remove
                </button>
              ) : (
                <span className="tag dim" title="The framework and environment define the project. They cannot be removed">
                  core
                </span>
              )}
            </div>
          ))}
        </div>
      ) : (
        <p className="muted" style={{ fontSize: 12.5, margin: '8px 0 0' }}>
          Nothing added yet. This project has only its framework and environment. Add a service below.
        </p>
      )}
    </div>
  )
}

// AddableCard offers the recipes compatible with this framework that are not yet
// installed, grouped by kind — the old add-only surface, now the second half.
function AddableCard({ d, dir, onChanged }: { d: AddableResult; dir: string; onChanged: () => void }) {
  const con = useConsole()
  const list = d.addable || []
  // addRecipe — stream keel add. --yes skips the TTY confirm the exec path cannot
  // answer; --trust covers a pack recipe.
  const addRecipe = async (id: string) => {
    await con.stream('/api/exec', { dir, args: ['add', id, '--yes', '--trust'] })
    onChanged()
  }
  if (d.error && !list.length) {
    return (
      <div className="card" style={{ marginTop: 14 }}>
        <h3 style={{ marginTop: 0 }}>Add a service</h3>
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
          {d.error}
        </p>
      </div>
    )
  }
  if (!list.length) {
    return (
      <div className="card" style={{ marginTop: 14 }}>
        <h3 style={{ marginTop: 0 }}>Add a service</h3>
        <p className="muted" style={{ fontSize: 13, margin: 0 }}>
          Nothing left to add. Every compatible recipe is already installed.
        </p>
      </div>
    )
  }
  // Group by kind, preserving first-seen order (the original built byKind by
  // iterating the list, then Object.keys — insertion order for string keys).
  const byKind: Record<string, Recipe[]> = {}
  const order: string[] = []
  for (const r of list) {
    const k = r.kind || ''
    if (!byKind[k]) {
      byKind[k] = []
      order.push(k)
    }
    byKind[k].push(r)
  }
  return (
    <div className="card" style={{ marginTop: 14 }}>
      <h3 style={{ marginTop: 0 }}>Add a service</h3>
      <p className="muted" style={{ fontSize: 12.5, margin: '0 0 4px' }}>
        Runs <code>keel add &lt;recipe&gt;</code>. Resolves against the existing stack, installs only the delta, records
        it in the manifest. Output streams below.
      </p>
      {order.map((k) => (
        <div style={{ marginTop: 12 }} key={k}>
          <div className="railcap" style={{ padding: '0 0 6px' }}>
            {KIND_LABEL[k] || k}
          </div>
          <div className="opts">
            {byKind[k].map((r) => (
              <span
                className="opt"
                key={r.id}
                onClick={() => addRecipe(r.id)}
                title={r.id}
              >
                <Icon r={r} size={18} /> {r.label || r.id}
              </span>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

export default function ManageTab({ p }: { p: Project | Member; be: Backend | null }) {
  const qc = useQueryClient()
  const dir = p.path

  // Both reads in parallel: installed (list + remove) and addable (add). Each
  // uses fetchJSON so a thrown fetch degrades to {error} inline rather than
  // blanking the tab, exactly like the original's fetchJSON pair.
  const installedQ = useQuery({
    queryKey: ['installed', dir],
    queryFn: () => fetchJSON<InstalledResult>('/api/installed?dir=' + encodeURIComponent(dir)),
    retry: false,
  })
  const addableQ = useQuery({
    queryKey: ['addable', dir],
    queryFn: () => fetchJSON<AddableResult>('/api/addable?dir=' + encodeURIComponent(dir)),
    retry: false,
  })

  // Re-read both cards so an added/removed recipe moves between Installed and the
  // addable list without a manual refresh (the original re-rendered the tab).
  const onChanged = () => {
    qc.invalidateQueries({ queryKey: ['installed', dir] })
    qc.invalidateQueries({ queryKey: ['addable', dir] })
  }

  // In flight: the same calm loading card the original paints before the reads
  // land.
  if (installedQ.isLoading || addableQ.isLoading) {
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Manage services · {p.name}</h3>
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
          Loading this project's recipes…
        </p>
      </div>
    )
  }

  const inst: InstalledResult = installedQ.data || {}
  const add: AddableResult = addableQ.data || {}

  // Both feeds errored: the shared error block (the original's inst.error &&
  // add.error path, which replaces the whole body).
  if (inst.error && add.error) {
    return <div className="err">{inst.error || add.error}</div>
  }

  return (
    <>
      <InstalledCard d={inst} dir={dir} onChanged={onChanged} />
      <AddableCard d={add} dir={dir} onChanged={onChanged} />
    </>
  )
}
