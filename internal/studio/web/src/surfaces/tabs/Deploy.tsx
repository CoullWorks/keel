import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiJSON } from '../../lib/api'
import type { Project, Member, Backend } from '../../lib/types'
import { useConsole } from '../../lib/console'

// Deploy is the React port of the studio's per-project DEPLOY tab (the original
// renderDeploy + renderDeployBody/pickDeployTarget in internal/studio/index.html,
// plus the DEPLOY state object). Pick a deploy target — defaulting to the
// profile's hosting default when it is one of the offered targets — see the
// artifacts that target writes, then generate them (or preview with a dry run).
//
// keel only ever GENERATES the deploy artifacts a hosting target needs; it never
// calls a cloud API — the note below the header says so, verbatim.
//
// Data: the targets feed (GET /api/deploy/targets?dir=) is keyed by the project
// path so switching focus refetches; the profile (GET /api/profile) supplies the
// hosting default the picker opens on. Both errors degrade to the same calm
// message-only cards the original paints.

type Focus = Project | Member

// --- payload shapes (from deploy.go). A target is one hosting option keel can
// generate artifacts for; the list rides along with the resolved framework. ---
type DeployTarget = { key: string; desc?: string; artifacts?: string[] }
type DeployTargets = { framework?: string; targets?: DeployTarget[]; error?: string }
// The profile carries the user's default hosting/deploy target (PROFILE.hosting).
type Profile = { hosting?: string }

// --- deploy action, ported from the original. renderDeployBody drives both
// buttons through the shared console (con.stream): POST /api/exec {dir,
// args:["deploy", <target>]} to generate, and the same with a trailing
// "--dry-run" to preview. deployArgs builds the args either way. ---
const deployArgs = (target: string, dryRun: boolean) =>
  dryRun ? ['deploy', target, '--dry-run'] : ['deploy', target]

export default function DeployTab({ p, be: _be }: { p: Project | Member; be: Backend | null }) {
  const con = useConsole()
  const dir = p.path

  // The offered targets + resolved framework (renderDeploy's fetchJSON). Keyed by
  // the project path so switching focus refetches.
  const targetsQ = useQuery({
    queryKey: ['deploy-targets', dir],
    queryFn: () => apiJSON<DeployTargets>('/api/deploy/targets?dir=' + encodeURIComponent(dir)),
    retry: false,
  })
  // The profile's hosting default — the picker opens on it when it is one of the
  // offered targets. Not keyed by path (the profile is global to the studio).
  const profileQ = useQuery({
    queryKey: ['profile'],
    queryFn: () => apiJSON<Profile>('/api/profile'),
    retry: false,
  })

  // The chosen target. null until the user picks; the render resolves the default
  // (profile hosting, else the first target) each pass until then, mirroring the
  // original DEPLOY.target defaulting in renderDeploy.
  const [picked, setPicked] = useState<string | null>(null)

  // In flight: the same calm "Loading deploy targets…" the original paints first.
  if (targetsQ.isLoading || (!targetsQ.data && !targetsQ.isError)) {
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Deploy {p.name}</h3>
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
          Loading deploy targets…
        </p>
      </div>
    )
  }

  // A thrown fetch degrades to a message-only payload (apiJSON throws; the
  // original's fetchJSON returns {error}). Either way, targets is empty.
  const d: DeployTargets = targetsQ.isError
    ? { targets: [], error: String((targetsQ.error as Error)?.message || targetsQ.error) }
    : targetsQ.data || {}
  const targets = d.targets || []
  const framework = d.framework || ''

  // Error with no targets: the calm "Could not read the deploy targets" card.
  if (d.error && !targets.length) {
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Deploy {p.name}</h3>
        <p className="muted" style={{ fontSize: 13, margin: 0 }}>
          Could not read the deploy targets: {d.error}
        </p>
      </div>
    )
  }
  // No targets at all: the calm "no deploy targets" card.
  if (!targets.length) {
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Deploy {p.name}</h3>
        <p className="muted" style={{ fontSize: 13, margin: 0 }}>
          keel has no deploy targets for this project.
        </p>
      </div>
    )
  }

  // Default to the profile's hosting default when it is one of the offered
  // targets, else the first target — so the picker opens on a sensible choice
  // (renderDeploy: keys.includes(PROFILE.hosting) ? PROFILE.hosting : keys[0]).
  const keys = targets.map((t) => t.key)
  const hosting = profileQ.data?.hosting
  const defaultTarget = hosting && keys.includes(hosting) ? hosting : keys[0]
  const target = picked && keys.includes(picked) ? picked : defaultTarget

  const cur = targets.find((t) => t.key === target) || targets[0]

  // The artifacts a target writes come from the endpoint; without a manifest the
  // framework is unknown so keel deploy --json omits them — say so rather than
  // imply the target writes nothing (renderDeployBody).
  const arts = (cur && cur.artifacts) || []

  return (
    <div className="card">
      <h3 style={{ marginTop: 0 }}>Deploy {p.name}</h3>
      <p className="muted" style={{ fontSize: 12.5, margin: '0 0 12px' }}>
        keel generates the deploy artifacts your hosting target needs. It never calls a cloud API. Output streams below.
      </p>
      <div className="railcap" style={{ padding: '0 0 6px' }}>
        Target
      </div>
      <div className="rlist">
        {targets.map((t) => {
          const on = t.key === target
          return (
            <div
              key={t.key}
              className={'ritem' + (on ? ' on' : '')}
              tabIndex={0}
              role="radio"
              aria-checked={on}
              onClick={() => setPicked(t.key)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  setPicked(t.key)
                }
              }}
            >
              <span className="dot" />
              <span className="rl">
                <b>{t.key}</b>
                {t.desc ? <small>{t.desc}</small> : null}
              </span>
            </div>
          )
        })}
      </div>
      <div style={{ marginTop: 14 }}>
        <div className="railcap" style={{ padding: '0 0 2px' }}>
          Artifacts <b style={{ color: 'var(--headSoft)' }}>{cur ? cur.key : ''}</b> generates
        </div>
        {arts.length ? (
          <ul style={{ margin: '6px 0 0', paddingLeft: 18 }}>
            {arts.map((f) => (
              <li key={f}>
                <code>{f}</code>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted" style={{ fontSize: 12.5, margin: '6px 0 0' }}>
            The exact files depend on this project's framework. keel resolves them at generate time.{' '}
            {framework ? '' : 'Build or track this project so keel knows its stack.'}
          </p>
        )}
      </div>
      <div className="row" style={{ gap: 8, marginTop: 14 }}>
        <button className="btn primary" onClick={() => con.stream('/api/exec', { dir, args: deployArgs(target, false) })}>
          ⤴ Generate deploy artifacts
        </button>
        <button className="btn" onClick={() => con.stream('/api/exec', { dir, args: deployArgs(target, true) })}>
          Preview (dry run)
        </button>
      </div>
    </div>
  )
}
