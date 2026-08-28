import { useSyncExternalStore } from 'react'
import type { Project } from './types'

// The studio's hash-route scheme, ported verbatim so every existing deep link
// round-trips:
//   #/                         home (projects)
//   #/build #/packs #/plugins  top-level views
//   #/settings[/<tab>]         settings, optional sub-tab
//   #/p/<slug>/<ptab>          a focused project + tab
//   #/p/<slug>/screen/<id>     a plugin per-project screen tab
//   #/extend/<id>              a global plugin page
export type Route =
  | { view: 'home' }
  | { view: 'build' }
  | { view: 'packs' }
  | { view: 'plugins' }
  | { view: 'settings'; tab?: string }
  | { view: 'project'; slug: string; ptab: string; screenId?: string }
  | { view: 'ppage'; pageId: string }

const TOP = new Set(['home', 'build', 'packs', 'settings', 'plugins'])

export function parseHash(hash: string): Route {
  const raw = (hash || location.hash || '#/').replace(/^#\/?/, '')
  const parts = raw.split('/').filter(Boolean).map(decodeURIComponent)
  if (!parts.length) return { view: 'home' }
  if (parts[0] === 'p' && parts[1]) {
    if (parts[2] === 'screen' && parts[3]) return { view: 'project', slug: parts[1], ptab: 'screen:' + parts[3], screenId: parts[3] }
    return { view: 'project', slug: parts[1], ptab: parts[2] || 'overview' }
  }
  if (parts[0] === 'extend' && parts[1]) return { view: 'ppage', pageId: parts[1] }
  const v = parts[0]
  if (v === 'settings') return { view: 'settings', tab: parts[1] }
  if (TOP.has(v)) return { view: v as 'home' | 'build' | 'packs' | 'plugins' }
  return { view: 'home' }
}

// projectSlug emits the friendly form: a project's name when it is unique among
// tracked top-level projects, else the encoded path (so members and name
// collisions still resolve). Mirrors the original projectSlug/serialise.
export function projectSlug(p: Project, projects: Project[]): string {
  const sameName = projects.filter((x) => x.name === p.name)
  if (sameName.length === 1 && projects.some((x) => x.path === p.path)) return encodeURIComponent(p.name)
  return encodeURIComponent(p.path)
}

// slugToPath resolves a #/p/<seg> segment back to a project path.
export function slugToPath(seg: string, projects: Project[]): string {
  const colon = seg.indexOf(':')
  if (colon > 0) {
    const pn = seg.slice(0, colon)
    const mn = seg.slice(colon + 1)
    const parents = projects.filter((p) => p.name === pn)
    if (parents.length === 1) {
      const m = (parents[0].members || []).find((x) => x.name === mn)
      if (m) return m.path
    }
  }
  const tops = projects.filter((p) => p.name === seg)
  if (tops.length === 1) return tops[0].path
  const members: { path: string }[] = []
  for (const p of projects) for (const m of p.members || []) if (m.name === seg) members.push(m)
  if (!tops.length && members.length === 1) return members[0].path
  return seg
}

export function navTo(hash: string): void {
  const h = hash.startsWith('#') ? hash : '#' + hash
  if (location.hash !== h) location.hash = h
}

function subscribe(cb: () => void): () => void {
  window.addEventListener('hashchange', cb)
  window.addEventListener('popstate', cb)
  return () => {
    window.removeEventListener('hashchange', cb)
    window.removeEventListener('popstate', cb)
  }
}

export function useRoute(): Route {
  const hash = useSyncExternalStore(
    subscribe,
    () => location.hash || '#/',
    () => '#/',
  )
  return parseHash(hash)
}
