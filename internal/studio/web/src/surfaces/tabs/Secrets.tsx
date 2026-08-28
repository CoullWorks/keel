import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { z } from 'zod'
import { apiJSON, fetchJSON } from '../../lib/api'
import type { Project, Member, Backend } from '../../lib/types'

// Secrets is the React port of the studio's Env & Secrets tab (renderSecrets +
// envVarRow/loadEnvVars/genSecret + magentoSecretRow/loadMagentoEnv). It lists
// the project's REAL resolved environment above the Sync/Generate actions, with
// secrets always masked ("••• present", never the value) and each variable's
// provenance shown; a Magento project also surfaces app/etc/env.xml, same rule.

// The effective backend the header + tabs read. Re-declared locally because
// ProjectDetail.tsx keeps it un-exported; kept lax — this tab does not read it,
// it only satisfies the shared tab signature.
// --- /api/env — one resolved variable + the response envelope. Mirrors the Go
// envVar/envResponse (env.go): secrets are value-masked before serialisation, so
// `value` is absent for a secret; `present` says the secret key has a value.
// Kept passthrough so unknown fields ride along, matching the studio's schemas.
const envVarSchema = z
  .object({
    key: z.string(),
    value: z.string().optional().default(''),
    secret: z.boolean().optional().default(false),
    public: z.boolean().optional().default(false),
    present: z.boolean().optional().default(false),
    file: z.string().optional().default(''),
    fromRoot: z.boolean().optional().default(false),
  })
  .passthrough()

const envResponseSchema = z
  .object({
    found: z.boolean().optional().default(false),
    vars: z.array(envVarSchema).optional().default([]),
    envDir: z.string().optional().default(''),
    fromRoot: z.boolean().optional().default(false),
    note: z.string().optional().default(''),
    error: z.string().optional().default(''),
  })
  .passthrough()

type EnvVar = z.infer<typeof envVarSchema>
type EnvResponse = z.infer<typeof envResponseSchema>

// --- /api/magento/env — one surfaced config key + its envelope. Mirrors the Go
// magentoConfigItem/magentoEnvResponse (magento_env.go): secret VALUES are
// withheld, only presence + provenance leave the process.
const magentoItemSchema = z
  .object({
    key: z.string(),
    file: z.string().optional().default(''),
    value: z.string().optional().default(''),
    secret: z.boolean().optional().default(false),
    present: z.boolean().optional().default(false),
  })
  .passthrough()

const magentoResponseSchema = z
  .object({
    installed: z.boolean().optional().default(false),
    envXml: z.string().optional().default(''),
    items: z.array(magentoItemSchema).optional().default([]),
    dotEnv: z.array(magentoItemSchema).optional().default([]),
    note: z.string().optional().default(''),
    error: z.string().optional().default(''),
  })
  .passthrough()

type MagentoItem = z.infer<typeof magentoItemSchema>
type MagentoResponse = z.infer<typeof magentoResponseSchema>

// isMagento gates the Magento config card, mirroring the original's
// /magento|mageos/i.test(PROJ.framework).
const isMagento = (fw: string) => /magento|mageos/i.test(fw)

// projExec fires POST /api/exec {dir, args} — the request the original's
// stream() sent for the Sync/Check/Generate actions. The SSE console is being
// ported separately; until then, faithful to ProjectDetail's convention, the
// button fires the same endpoint with the same body (fire-and-forget).
function projExec(dir: string, args: string[]) {
  return apiJSON('/api/exec', { dir, args }).catch(() => {})
}

// EmptyOrCode renders a value cell: a non-empty value in <code>, else the
// original's muted "empty" placeholder. Mirrors
// `<code>${esc(v.value)||'<span class="muted">empty</span>'}</code>` — note the
// original leaves the empty placeholder OUTSIDE the <code> because the ||
// falls through when the escaped value is empty.
function EmptyOrCode({ value }: { value: string }) {
  return value ? <code>{value}</code> : <span className="muted">empty</span>
}

// EnvVarRow renders one resolved variable. A public (NEXT_PUBLIC_) var shows its
// value with a "public" chip; a secret shows "••• present" (never the value)
// with a "secret" chip; ordinary config shows its value with a "config" chip.
// Every row names the file it resolved from, flagging a root-inherited value.
function EnvVarRow({ v }: { v: EnvVar }) {
  const chip = v.public ? (
    <span className="tag ok">public</span>
  ) : v.secret ? (
    <span className="tag warn">secret</span>
  ) : (
    <span className="tag dim">config</span>
  )
  const val = v.secret ? (
    v.present ? (
      <span className="tag ok">••• present</span>
    ) : (
      <span className="tag dim">not set</span>
    )
  ) : (
    <EmptyOrCode value={v.value || ''} />
  )
  return (
    <div className="plan-row" style={{ alignItems: 'center' }}>
      <div className="k" style={{ width: 220, wordBreak: 'break-all' }}>
        {v.key}
      </div>
      <div>{chip}</div>
      <div style={{ flex: 1 }}>{val}</div>
      <code
        style={{ fontSize: 11 }}
        title={v.fromRoot ? 'inherited from the workspace root' : 'this project'}
      >
        {v.file || ''}
        {v.fromRoot ? ' · root' : ''}
      </code>
    </div>
  )
}

// The intro paragraph above the resolved list — verbatim from the original.
const envIntro = (
  <p className="muted" style={{ fontSize: 12.5, margin: '0 0 12px' }}>
    The variables this project resolves, in Next.js precedence (<code>.env.*.local</code> &gt;{' '}
    <code>.env.local</code> &gt; <code>.env.&lt;env&gt;</code> &gt; <code>.env</code>).{' '}
    <b>NEXT_PUBLIC_</b> vars are shown in full; secrets are masked. Their value never leaves your
    machine.
  </p>
)

// EnvVars is loadEnvVars: reading / error / empty (found=false or no vars) /
// listed, in the same card with the same heading in every state.
function EnvVars({ dir }: { dir: string }) {
  const q = useQuery({
    queryKey: ['env', dir],
    queryFn: () => fetchJSON<EnvResponse>('/api/env?dir=' + encodeURIComponent(dir)),
    retry: false,
  })

  const heading = (
    <h3 style={{ marginTop: 0 }}>Environment variables</h3>
  )

  // Loading: the "reading env…" placeholder, no intro (matches the original's
  // initial innerHTML before the fetch resolves).
  if (q.isLoading || q.data === undefined) {
    return (
      <div className="card">
        {heading}
        <p className="muted" style={{ margin: 0 }}>
          reading env…
        </p>
      </div>
    )
  }

  const d = q.data as EnvResponse | { error: string }

  // A hard error (fetchJSON's {error}, or the response's own error field).
  if ('error' in d && d.error) {
    return (
      <div className="card">
        {heading}
        <div className="err">{d.error}</div>
      </div>
    )
  }

  const r = envResponseSchema.parse(d)

  // No env found (or found but empty): the intro + a calm note, never an error.
  if (!r.found || !(r.vars && r.vars.length)) {
    return (
      <div className="card">
        {heading}
        {envIntro}
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
          {r.note ||
            'No env found. Inherited from the workspace root or injected by the platform. Run Sync .env below to create one.'}
        </p>
      </div>
    )
  }

  return (
    <div className="card">
      {heading}
      {envIntro}
      {r.fromRoot && (
        <div className="msub" style={{ marginTop: 0 }}>
          inherited from the workspace root
          {r.envDir ? (
            <>
              {' · '}
              <code style={{ fontSize: 11 }}>{r.envDir}</code>
            </>
          ) : null}
        </div>
      )}
      {r.vars.map((v, i) => (
        <EnvVarRow key={v.key || i} v={v} />
      ))}
    </div>
  )
}

// MagentoSecretRow renders one surfaced config key. A secret shows "••• present"
// (or "not set") + the file it lives in — never the value; a non-secret shows
// its value. Mirrors the studio's rule: show WHERE a credential lives and THAT
// it exists, never the value.
function MagentoSecretRow({ it }: { it: MagentoItem }) {
  const val = it.secret ? (
    it.present ? (
      <span className="tag ok">••• present</span>
    ) : (
      <span className="tag dim">not set</span>
    )
  ) : (
    <EmptyOrCode value={it.value || ''} />
  )
  return (
    <div className="plan-row" style={{ alignItems: 'center' }}>
      <div className="k" style={{ width: 160 }}>
        {it.key}
      </div>
      <div style={{ flex: 1 }}>{val}</div>
      <code style={{ fontSize: 11 }}>{it.file}</code>
    </div>
  )
}

// The intro paragraph for the Magento card — verbatim from the original.
const magentoIntro = (
  <p className="muted" style={{ fontSize: 12.5, margin: '0 0 12px' }}>
    The DB connection, crypt key and cache/session backends from <code>app/etc/env.xml</code>. Secret
    values (password, crypt key) are never shown: only that they exist and where they live.
  </p>
)

// The .env-keys sub-list, shared by the not-installed and installed states.
function DotEnvKeys({ dotEnv }: { dotEnv: MagentoItem[] }) {
  if (!dotEnv.length) return null
  return (
    <>
      <div className="msub" style={{ marginTop: 16 }}>
        .env keys
      </div>
      {dotEnv.map((it, i) => (
        <MagentoSecretRow key={it.key || i} it={it} />
      ))}
    </>
  )
}

// MagentoEnv is loadMagentoEnv: reading / error / not-installed / parsed, in the
// same card with the same heading in every state.
function MagentoEnv({ dir }: { dir: string }) {
  const q = useQuery({
    queryKey: ['magento-env', dir],
    queryFn: () => fetchJSON<MagentoResponse>('/api/magento/env?dir=' + encodeURIComponent(dir)),
    retry: false,
  })

  const heading = <h3 style={{ marginTop: 0 }}>Magento config (app/etc/env.xml)</h3>

  if (q.isLoading || q.data === undefined) {
    return (
      <div className="card">
        {heading}
        <p className="muted" style={{ margin: 0 }}>
          reading Magento config…
        </p>
      </div>
    )
  }

  const d = q.data as MagentoResponse | { error: string }

  if ('error' in d && d.error) {
    return (
      <div className="card">
        {heading}
        <div className="err">{d.error}</div>
      </div>
    )
  }

  const r = magentoResponseSchema.parse(d)

  // Not installed: the intro + a note, plus any .env keys found alongside.
  if (!r.installed) {
    return (
      <div className="card">
        {heading}
        {magentoIntro}
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
          {r.note || "No app/etc/env.xml found. Magento isn't configured yet."}
        </p>
        <DotEnvKeys dotEnv={r.dotEnv} />
      </div>
    )
  }

  return (
    <div className="card">
      {heading}
      {magentoIntro}
      {r.items && r.items.length ? (
        r.items.map((it, i) => <MagentoSecretRow key={it.key || i} it={it} />)
      ) : (
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
          env.xml parsed but no recognised config keys found.
        </p>
      )}
      <DotEnvKeys dotEnv={r.dotEnv} />
    </div>
  )
}

export default function SecretsTab({ p }: { p: Project | Member; be: Backend | null }) {
  // genkey drives the "Generate a key" action, replacing the original's DOM read
  // of #genkey.value.
  const [genKey, setGenKey] = useState('')

  const genSecret = () => {
    const k = genKey.trim()
    if (!k) {
      alert('Enter a key name.')
      return
    }
    projExec(p.path, ['secrets', 'generate', k])
  }

  return (
    <div>
      <p className="lede" style={{ marginTop: 0 }}>
        Manage <b>{p.name}</b>'s environment and secrets. keel generates the .env keys the stack
        needs and app keys locally. It never prints a secret value into the console. Values stream to
        disk with owner-only permissions.
      </p>

      <EnvVars dir={p.path} />

      <div className="card">
        <h3>Sync .env</h3>
        <p className="muted" style={{ fontSize: 12.5, margin: '0 0 12px' }}>
          Creates .env from .env.example, fills in required keys, and generates any app/secret keys
          the framework needs (APP_KEY, etc.).
        </p>
        <div className="row" style={{ gap: 8 }}>
          <button className="btn primary" onClick={() => projExec(p.path, ['secrets', 'sync'])}>
            Sync secrets
          </button>
          <button className="btn" onClick={() => projExec(p.path, ['secrets', 'check'])}>
            Check
          </button>
        </div>
      </div>

      <div className="card">
        <h3>Generate a key</h3>
        <p className="muted" style={{ fontSize: 12.5, margin: '0 0 12px' }}>
          Generate a value for a single key and write it to .env.
        </p>
        <div className="row" style={{ gap: 8 }}>
          <input
            id="genkey"
            placeholder="KEY_NAME (e.g. APP_KEY)"
            style={{ flex: 1 }}
            value={genKey}
            onChange={(e) => setGenKey(e.target.value)}
          />
          <button className="btn" onClick={genSecret}>
            Generate
          </button>
        </div>
      </div>

      {isMagento(p.framework) && <MagentoEnv dir={p.path} />}
    </div>
  )
}
