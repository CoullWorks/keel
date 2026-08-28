import { useEffect, useRef, useState } from 'react'
import { api } from './api'

// PluginComponent mounts a plugin's own built React component — the no-iframe,
// no-injection own-UI tier. keel serves the bundle at /plugin-assets/<name>/…
// (trusted-only, path-safe); we dynamic-import it and call its mount(el, keel).
// The bundle brings its own React and renders into `el`; keel.call(action, args)
// → /api/plugin/call runs the work in the plugin's own process, trust- +
// capability-gated.
//
// TRUST BOUNDARY, stated honestly: the bundle loads only AFTER the user trusts the
// plugin (keel refuses to serve an untrusted plugin's assets), and it then runs in
// the studio's own page realm — so a trusted plugin CAN reach the same-origin DOM
// and API directly, not only through the `keel` object. Trusting a plugin means
// trusting its code with the studio session, exactly as trusting it lets keel run
// its executables. The `keel` object is the intended, convenient channel, not a
// sandbox. Real isolation (Worker / sandboxed iframe) is deliberately not used —
// the architecture is no-iframe by choice.

// A tracked project, as keel.projects() hands it to a component (the subset a
// picker needs).
export type KeelProject = { name: string; path: string; framework?: string; env?: string; managed?: boolean }

// Keel is the client handed to a plugin component. call() runs one of the
// plugin's declared actions and returns its JSON result (unwrapping {ok,result}).
// projects() lists the user's tracked projects — host data the studio provides so
// a component can offer a project picker; it stays read-only and the studio keeps
// the token (the component never touches the API directly). dir is the project a
// component SCREEN is scoped to (undefined for a global page); call() threads it
// so the plugin's action runs against the right project.
export type Keel = {
  plugin: string
  dir?: string
  call: (action: string, args?: Record<string, unknown>) => Promise<unknown>
  projects: () => Promise<KeelProject[]>
}

// The shape a plugin bundle exports: a mount (and optional unmount), either named
// or as the default export.
type PluginModule = {
  mount?: (el: HTMLElement, keel: Keel) => void
  unmount?: (el: HTMLElement) => void
  default?: (el: HTMLElement, keel: Keel) => void
}

function makeKeel(plugin: string, dir?: string): Keel {
  return {
    plugin,
    dir,
    async call(action, args) {
      const r = await api('/api/plugin/call', {
        method: 'POST',
        // dir is threaded for a project-scoped component screen so the action runs
        // against its project; a global page passes none and the field is omitted.
        body: JSON.stringify({ plugin, action, args: args || {}, ...(dir ? { dir } : {}) }),
      })
      if (!r.ok) {
        const t = await r.text().catch(() => '')
        throw new Error(r.status + (t ? ' ' + t.trim() : ''))
      }
      const d = (await r.json().catch(() => ({}))) as { ok?: boolean; error?: string; result?: unknown }
      if (d && d.ok === false) throw new Error(d.error || 'failed')
      return d ? d.result : null
    },
    async projects() {
      const r = await api('/api/projects')
      if (!r.ok) return []
      const d = (await r.json().catch(() => ({}))) as { projects?: KeelProject[] }
      return d.projects || []
    },
  }
}

export function PluginComponent({ url, plugin, dir }: { url: string; plugin: string; dir?: string }) {
  const hostRef = useRef<HTMLDivElement>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let cancelled = false
    let mod: PluginModule | null = null
    setErr('')
    // A runtime plugin URL, not a build-time import — Vite must leave it alone.
    import(/* @vite-ignore */ url)
      .then((m: PluginModule) => {
        if (cancelled || !hostRef.current) return
        mod = m
        const mount = m.mount || m.default
        if (typeof mount !== 'function') {
          setErr('This plugin bundle does not export a mount(el, keel) function.')
          return
        }
        // A throwing mount() must surface as an error here, not as an unhandled
        // rejection that escapes React. (Errors thrown later, inside the plugin's
        // own React lifecycle, are caught by the ErrorBoundary this is wrapped in.)
        try {
          mount(hostRef.current, makeKeel(plugin, dir))
        } catch (e) {
          setErr('This plugin failed to start: ' + String((e as Error).message || e))
        }
      })
      .catch((e) => {
        if (!cancelled)
          setErr('Could not load the plugin UI — is the plugin trusted? (' + String((e as Error).message || e) + ')')
      })
    return () => {
      cancelled = true
      try {
        if (mod && typeof mod.unmount === 'function') mod.unmount(host)
      } catch {
        /* ignore teardown errors */
      }
    }
  }, [url, plugin, dir])

  if (err) return <div className="err">{err}</div>
  return <div ref={hostRef} className="pluginmount" />
}
