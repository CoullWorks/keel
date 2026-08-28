import { createContext, useContext, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from './api'

// The studio console: a single shared drawer + SSE streamer, ported from the
// original stream()/termLine()/toast() machinery. Any surface calls
// useConsole().stream(url, body) to run a keel command and see its output stream
// live, with a running→ok/bad toast and a pass/fail that reveals the drawer.

type Line = { text: string; cls: string }
type ToastKind = 'info' | 'running' | 'ok' | 'bad'
type Toast = { id: string; kind: ToastKind; msg: string }

const TERM_MAX = 2000
const TOAST_TTL = 5000
const toastIcon: Record<ToastKind, string> = { info: 'ℹ', running: '⟳', ok: '✓', bad: '✗' }

function lineClass(line: string): string {
  if (line.startsWith('✓')) return 'ok'
  if (line.startsWith('✗')) return 'bad'
  if (line.startsWith('→') || line.startsWith('$')) return 'cmd'
  if (line.startsWith('keel ')) return 'hd'
  return ''
}

// STATIC_QUERY_KEYS are the query-key roots a streamed command cannot change, so
// they are skipped when refreshing live surfaces after a stream (see the
// invalidateQueries call). They are configuration/catalogue, not project state:
// the recipe catalogue (expensive to rebuild), the engineer profile, the global
// brand, the deploy-target list, and the addable-recipe list.
const STATIC_QUERY_KEYS = new Set(['recipes', 'profile', 'brand-global', 'deploy-targets', 'addable'])

// streamLabel derives a human name for a streamed command so a toast says WHAT
// ran rather than echoing a URL (matches the original).
function streamLabel(url: string, body: { args?: unknown[]; name?: string } | null): string {
  body = body || {}
  if (Array.isArray(body.args) && body.args.length) return 'keel ' + body.args.join(' ')
  if (body.name && /build/i.test(url)) return 'Building ' + body.name
  if (/build/i.test(url)) return 'Building'
  return url
}

export interface ConsoleApi {
  lines: Line[]
  live: boolean
  title: string
  sub: string
  open: boolean
  heightPx: number
  toasts: Toast[]
  stream: (url: string, body: unknown) => Promise<unknown>
  clear: () => void
  stop: () => void
  setTitle: (title: string, sub?: string) => void
  setOpen: (o: boolean) => void
  setHeight: (px: number) => void
  toast: (msg: string, opts?: { kind?: ToastKind; id?: string; sticky?: boolean }) => string
  dismissToast: (id: string) => void
}

const Ctx = createContext<ConsoleApi | null>(null)

export function useConsole(): ConsoleApi {
  const c = useContext(Ctx)
  if (!c) throw new Error('useConsole must be used within <ConsoleProvider>')
  return c
}

export function ConsoleProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const [lines, setLines] = useState<Line[]>([])
  const [live, setLive] = useState(false)
  const [title, setTitleState] = useState('Console')
  const [sub, setSub] = useState('')
  const [open, setOpen] = useState(true)
  const [heightPx, setHeight] = useState(() => Math.round(window.innerHeight * 0.24))
  const [toasts, setToasts] = useState<Toast[]>([])

  const abortRef = useRef<AbortController | null>(null)
  const seqRef = useRef(0)
  const timers = useRef<Record<string, ReturnType<typeof setTimeout>>>({})

  const dismissToast = (id: string) => {
    const t = timers.current[id]
    if (t) {
      clearTimeout(t)
      delete timers.current[id]
    }
    setToasts((ts) => ts.filter((x) => x.id !== id))
  }

  const toast: ConsoleApi['toast'] = (msg, opts = {}) => {
    const kind = opts.kind || 'info'
    const id = opts.id || 'toast' + ++seqRef.current
    if (timers.current[id]) {
      clearTimeout(timers.current[id])
      delete timers.current[id]
    }
    setToasts((ts) => {
      const next = ts.filter((x) => x.id !== id)
      next.push({ id, kind, msg })
      return next
    })
    const sticky = opts.sticky || kind === 'running'
    if (!sticky) timers.current[id] = setTimeout(() => dismissToast(id), TOAST_TTL)
    return id
  }

  const clear = () => setLines([])

  const pushLine = (text: string) =>
    setLines((ls) => {
      const next = ls.length >= TERM_MAX ? ls.slice(ls.length - TERM_MAX + 1) : ls.slice()
      next.push({ text, cls: lineClass(text) })
      return next
    })

  const setTitle = (t: string, s = '') => {
    setTitleState(t)
    setSub(s)
  }

  const reveal = () => {
    setOpen(true)
    setHeight((h) => Math.max(h, Math.round(window.innerHeight * 0.4)))
  }

  const stop = () => {
    if (abortRef.current) {
      try {
        abortRef.current.abort()
      } catch {
        /* ignore */
      }
      pushLine('■ stopped')
      toast('Stopped', { kind: 'info', id: 'stream-toast' })
    }
  }

  const stream: ConsoleApi['stream'] = async (url, body) => {
    if (abortRef.current) {
      try {
        abortRef.current.abort()
      } catch {
        /* ignore */
      }
    }
    const ac = new AbortController()
    abortRef.current = ac
    setLines([])
    setLive(true)
    setOpen(true)
    const label = streamLabel(url, body as { args?: unknown[]; name?: string })
    const tid = 'stream-toast'
    toast(label + '…', { kind: 'running', id: tid })
    let consent: unknown = null
    let fin: { ok?: boolean; code?: number; err?: string } | null = null
    try {
      const res = await api(url, { method: 'POST', body: JSON.stringify(body), signal: ac.signal })
      if (!res.ok) {
        pushLine('✗ ' + res.status + ' ' + res.statusText)
        toast(label + ' failed (' + res.status + ')', { kind: 'bad', id: tid })
        return null
      }
      const reader = res.body!.getReader()
      const dec = new TextDecoder()
      let buf = ''
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buf += dec.decode(value, { stream: true })
        const frames = buf.split('\n\n')
        buf = frames.pop() || ''
        for (const f of frames) {
          // Parse each SSE frame per spec: collect the event name and ALL data:
          // lines (a frame may carry several, which concatenate with a newline),
          // regardless of field order. The old prefix-match only saw the first
          // data: line and assumed event: came first.
          let event = ''
          const dataLines: string[] = []
          for (const line of f.split('\n')) {
            if (line.startsWith('event:')) event = line.slice(6).trim()
            else if (line.startsWith('data:')) dataLines.push(line.slice(5).replace(/^ /, ''))
          }
          const data = dataLines.join('\n')
          if (event === 'done') {
            try {
              fin = JSON.parse(data)
            } catch {
              /* ignore */
            }
          } else if (event === 'consent') {
            try {
              consent = JSON.parse(data)
            } catch {
              /* ignore */
            }
          } else if (dataLines.length) {
            pushLine(data)
          }
        }
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') {
        pushLine('✗ ' + e)
        toast(label + ' failed', { kind: 'bad', id: tid })
      }
    } finally {
      if (abortRef.current === ac) {
        abortRef.current = null
        setLive(false)
      }
      if (fin) {
        if (fin.ok) {
          toast(label + ' finished', { kind: 'ok', id: tid })
        } else {
          const code = fin.code ? ' (exit ' + fin.code + ')' : ''
          const why = fin.err ? ': ' + fin.err : ''
          toast(label + ' failed' + code + why, { kind: 'bad', id: tid })
          reveal()
        }
      }
      // A stream can change on-disk state (a build, an add, a start); refresh the
      // live surfaces so the rail/header/tabs reflect it — but NOT the static
      // config queries a stream can't affect (the recipe catalogue, profile,
      // global brand, deploy targets, the addable-recipe list). Skipping those
      // avoids a refetch storm after every action, in particular re-building the
      // recipe catalogue (a whole-home walk) on each command. Everything dynamic
      // still refreshes, so nothing goes stale.
      qc.invalidateQueries({
        predicate: (q) => !STATIC_QUERY_KEYS.has(String(q.queryKey[0])),
      })
    }
    return consent
  }

  const value: ConsoleApi = {
    lines,
    live,
    title,
    sub,
    open,
    heightPx,
    toasts,
    stream,
    clear,
    stop,
    setTitle,
    setOpen,
    setHeight,
    toast,
    dismissToast,
  }
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export { toastIcon }
export type { Toast }
