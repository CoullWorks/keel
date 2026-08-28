import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiJSON } from '../../lib/api'
import type { Project, Member, Backend } from '../../lib/types'
import { useConsole } from '../../lib/console'
import { clickable } from '../../lib/a11y'

// Run is the React port of the studio's per-project RUN & LOGS tab (the original
// renderRun + renderRunHost/pickTask/doRun helpers in internal/studio/index.html
// lines 2578–2619). It lists a stack's runnable tasks (dev/test/lint/typecheck/
// build) as a picker and lets the user start one / stop it, with the live output
// streaming below in the original.
//
// Two top-level shapes, ported verbatim from renderRun:
//   - a root-launch member (isRootLaunch): the workspace launches from its root,
//     so the member has no per-env task set — only the single root launch command.
//   - every other project/member: the per-framework task picker.
//
// The Run button streams through the shared console (con.stream("/api/run", …))
// so stdout/stderr render live below, and Stop aborts that stream (con.stop()).

type Focus = Project | Member

// runTask mirrors the server's runTask struct (console.go): {name, command}.
type RunTask = { name: string; command: string }
// RunTasks is the /api/run/tasks payload — the framework/env labels + the task
// list, or a message-only {error} the original falls back to on a thrown fetch.
type RunTasks = { framework?: string; env?: string; tasks?: RunTask[]; error?: string }

// isRootLaunch — the focused project/member runs from its workspace root; read
// off the effective-backend answer (mirrors the original isRootLaunch()).
const isRootLaunch = (be: Backend | null) => !!(be && be.rootLaunch)

export default function RunTab({ p, be }: { p: Project | Member; be: Backend | null }) {
  // A root-launch member runs from the workspace root, not its own env: its only
  // task is the launch itself, mapping to one root package-manager command. Show
  // that instead of the per-framework dev/test/lint set (renderRun isRootLaunch).
  if (isRootLaunch(be)) {
    return <RootLaunch p={p} be={be} />
  }
  return <TaskPicker p={p} />
}

function RootLaunch({ p, be }: { p: Focus; be: Backend | null }) {
  const con = useConsole()
  const cmd = (be && be.launchCommand) || ((be && be.launchManager) || 'pnpm') + ' dev'
  return (
    <>
      <p className="lede" style={{ marginTop: 0 }}>
        Launched from the workspace root. <b>{p.name}</b> runs together with the other members via <code>{cmd}</code>.
        There is no per-member environment to start; keel runs the root command.
      </p>
      <div className="card">
        <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
          <span className="tag warn">root-launch</span>
          <span className="tag">{(be && be.launchManager) || 'workspace'}</span>
        </div>
        <div className="runbar" style={{ marginTop: 16 }}>
          <button className="btn primary" onClick={() => con.stream('/api/run', { dir: p.path, task: 'dev' })}>
            ▷ Run {cmd}
          </button>
          <button className="btn ghost" onClick={() => con.stop()}>
            ■ Stop
          </button>
          <span className="muted" style={{ fontSize: 12 }}>
            runs: keel run dev → {cmd}
          </span>
        </div>
      </div>
    </>
  )
}

function TaskPicker({ p }: { p: Focus }) {
  // The selected task drives the Run button label + enabled state (RUN.task).
  const [task, setTask] = useState<string | null>(null)

  // The task list, keyed by the project path so switching focus refetches. The
  // original POSTs /api/run/tasks {dir}; keep that exact method + body.
  const tasksQ = useQuery({
    queryKey: ['run-tasks', p.path],
    queryFn: () => apiJSON<RunTasks>('/api/run/tasks', { dir: p.path }),
    retry: false,
  })

  const lede = (
    <p className="lede" style={{ marginTop: 0 }}>
      Run <b>{p.name}</b>'s tasks (dev, test, lint, typecheck, build) through its own environment, and watch stdout/stderr
      stream live below. Stop halts the task.
    </p>
  )

  // In flight: the original paints "loading tasks…" into #runhost.
  if (tasksQ.isLoading || (!tasksQ.data && !tasksQ.isError)) {
    return (
      <>
        {lede}
        <div id="runhost">
          <span className="muted">loading tasks…</span>
        </div>
      </>
    )
  }

  // A thrown fetch degrades to a message-only result — the same {error} shape
  // the original fetchJSON falls back to — surfaced in the .err block below.
  const d: RunTasks = tasksQ.isError
    ? { tasks: [], error: String((tasksQ.error as Error)?.message || tasksQ.error) }
    : tasksQ.data || {}

  return (
    <>
      {lede}
      <div id="runhost">
        <RunHost d={d} p={p} task={task} onPick={setTask} />
      </div>
    </>
  )
}

// RunHost is the React port of renderRunHost — the error / empty / task-picker
// states painted into #runhost.
function RunHost({
  d,
  p,
  task,
  onPick,
}: {
  d: RunTasks
  p: Focus
  task: string | null
  onPick: (name: string) => void
}) {
  const con = useConsole()
  if (d.error) {
    return <div className="err">{d.error}</div>
  }
  const tasks = d.tasks || []
  if (!tasks.length) {
    return <div className="muted">This stack defines no runnable tasks.</div>
  }
  return (
    <div className="card">
      <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
        <span className="tag">{d.framework || ''}</span>
        <span className="tag">{d.env || ''}</span>
      </div>
      <div className="taskchips">
        {tasks.map((t) => (
          <div
            key={t.name}
            className={'taskchip ' + (task === t.name ? 'on' : '')}
            {...clickable(() => onPick(t.name))}
          >
            <b>{t.name}</b>
            <small title={t.command}>{t.command}</small>
          </div>
        ))}
      </div>
      <div className="runbar" style={{ marginTop: 16 }}>
        <button
          className="btn primary"
          id="runbtn"
          disabled={!task}
          onClick={() => {
            if (task) con.stream('/api/run', { dir: p.path, task })
          }}
        >
          ▷ Run {task || 'task'}
        </button>
        <button className="btn ghost" onClick={() => con.stop()}>
          ■ Stop
        </button>
        <span className="muted" style={{ fontSize: 12 }}>
          {task ? 'runs: keel run ' + task : 'pick a task above'}
        </span>
      </div>
    </div>
  )
}
