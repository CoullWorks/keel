import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiJSON, fetchJSON } from '../lib/api'
import { Icon, iconSlug, iconTint, BRAND_SVG } from '../lib/icons'
import { useConsole } from '../lib/console'
import { navTo } from '../lib/router'

// Build.tsx — the React port of the studio's "New stack" BUILD WIZARD
// (renderBuild + its whole recipe-selection engine: buildSteps/buildStepBody/
// buildStepChosen/buildStepSummary, the sel state, applies/isDefault/ofKind/
// frameworksFor/families/forFw/byId/isWeb/webFor/svcFor/chosenWeb/setWeb,
// defaults/resetDeps, the conflict handling in toggleBuild, the plugin-options
// step, credentials + review, resolve(), and build()). It is PIXEL-IDENTICAL to
// the vanilla surface: the same accordion/rlist/mlist primitives, the same
// wrap2/colL/colR two-column layout, the same live plan panel + service graph,
// the same Back/Next floating bar and Review Build/Preview buttons.
//
// The engine is data-driven off the FULL recipe catalog (GET /api/recipes) plus
// the engineer profile (GET /api/profile, used to pre-seed defaults) and the
// applicable plugins' wizard options (GET /api/plugin-options?framework=…). The
// build itself streams through the shared console (con.stream('/api/build', …)),
// including the untrusted-pack consent handshake.

// ---------- recipe catalog shapes (mirror recipeDTO in studio.go) ------------
type Recipe = {
  id: string
  kind: string
  label: string
  lang?: string
  source?: string
  appliesTo?: string[]
  default?: boolean
  defaultFor?: string[]
  provides?: string[]
  conflicts?: string[]
  family?: string
  variant?: string
  category?: string
}
type RecipesResult = { recipes: Recipe[]; languages: string[] }

// ---------- profile (the subset defaults() reads; mirrors /api/profile) -------
type Profile = {
  framework?: string
  env?: string
  database?: string
  frontend?: string
  webserver?: string
  services?: string
  addons?: string
  extras?: string
}

// ---------- plugin wizard options (mirror plugin.OptionSchema / OptionChoice) -
type OptionChoice = { value: string; label?: string; description?: string; default?: boolean }
type OptionSchema = { id: string; label?: string; help?: string; type: string; choices?: OptionChoice[] }

// ---------- resolve() response (mirrors handleResolve) ------------------------
type PlanRecipe = { id: string; kind: string; label: string }
type ResolveResult = {
  framework?: string
  recipes?: PlanRecipe[]
  steps?: string[]
  credentials?: CredentialDTO[]
  envNames?: string[]
  error?: string
}

// ---------- credentials (mirror credentialDTO) -------------------------------
type CredentialDTO = {
  id: string
  kind: string
  label?: string
  help?: string
  required?: boolean
  auth?: string
  saved?: boolean
}
// A collected credential value posted with the build (mirrors creds.Value).
type CredValue = {
  id: string
  kind: string
  auth?: string
  username: string
  secret: string
  remember: boolean
}
// One user-added extra credential row (an .env key or a private Composer repo).
type ExtraCred = { kind: string; id: string; username: string; secret: string; remember: boolean }

// ---------- the sel selection state (the original `sel` singleton) ------------
type Sel = {
  lang: string | null
  framework: string | null
  env: string | null
  db: string | null
  frontend: string | null
  service: string[]
  addon: string[]
  extra: string[]
}

// One wizard step (mirrors the objects buildSteps() pushes).
type Step = {
  key: string
  title: string
  kind?: string
  multi?: boolean
  optional?: boolean
}

// The language display labels, ported verbatim.
const LANG_LABEL: Record<string, string> = { php: 'PHP', python: 'Python', node: 'Node', other: 'Other' }

// SlugIcon mirrors iconImg(slug, size) EXACTLY — it renders an ALREADY-resolved
// slug (the rlist/mlist items carry `iconSlug(recipe)` upfront, so re-resolving
// inside Icon() would be wrong for slugs that are not themselves search tokens).
// Empty slug → nothing, matching iconImg returning "".
function SlugIcon({ slug, size = 20 }: { slug?: string; size?: number }) {
  if (!slug) return null
  const s = size
  const box: React.CSSProperties = {
    width: s,
    height: s,
    borderRadius: 5,
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    background: iconTint[slug] || '#8b93a1',
    flex: 'none',
    verticalAlign: 'middle',
  }
  const p = BRAND_SVG[slug]
  if (p) {
    const gs = Math.round(s * 0.66)
    return (
      <span className="ic" title={slug} style={box}>
        <svg width={gs} height={gs} viewBox="0 0 24 24" fill="#fff" aria-hidden="true">
          <path d={p} />
        </svg>
      </span>
    )
  }
  return (
    <span
      className="ic"
      title={slug}
      style={{ ...box, lineHeight: `${s}px`, textAlign: 'center', fontSize: Math.round(s * 0.55), fontWeight: 700, color: '#0f1218' }}
    >
      {slug[0].toUpperCase()}
    </span>
  )
}

// linkify + the credentials help copy: turn bare URLs into links (ported).
function Linkify({ text }: { text: string }): ReactNode {
  const parts = text.split(/(https?:\/\/[^\s)]+)/g)
  return (
    <>
      {parts.map((p, i) =>
        /^https?:\/\//.test(p) ? (
          <a key={i} href={p} target="_blank" rel="noopener">
            {p}
          </a>
        ) : (
          <span key={i}>{p}</span>
        ),
      )}
    </>
  )
}

export default function Build() {
  // The recipe catalog + languages (the original R / LANGS). The build wizard is
  // useless without it, so a thrown fetch shows the calm inline error.
  const recipesQ = useQuery({
    queryKey: ['recipes'],
    queryFn: () => apiJSON<RecipesResult>('/api/recipes'),
  })
  // The engineer profile pre-seeds defaults for the framework the user picks
  // (defaults()). A failure just means no pre-seed — degrade, don't block — so
  // this uses fetchJSON and treats any error as an empty profile.
  const profileQ = useQuery({
    queryKey: ['profile'],
    queryFn: () => fetchJSON<Profile>('/api/profile'),
  })

  if (recipesQ.error) return <div className="err">{String((recipesQ.error as Error).message || recipesQ.error)}</div>
  if (!recipesQ.data || !profileQ.data) return <div className="muted">Loading…</div>

  const R = recipesQ.data.recipes || []
  const LANGS = recipesQ.data.languages || []
  const PROFILE: Profile = profileQ.data && !('error' in profileQ.data) ? (profileQ.data as Profile) : {}

  return <BuildWizard R={R} LANGS={LANGS} PROFILE={PROFILE} />
}

// BuildWizard owns the whole staged surface + its state, once the catalog is in.
function BuildWizard({ R, LANGS, PROFILE }: { R: Recipe[]; LANGS: string[]; PROFILE: Profile }) {
  const con = useConsole()

  // ---- the sel selection state (boot: sel.lang = LANGS[0], as the original) --
  const [sel, setSel] = useState<Sel>(() => ({
    lang: LANGS.length ? LANGS[0] : null,
    framework: null,
    env: null,
    db: null,
    frontend: null,
    service: [],
    addon: [],
    extra: [],
  }))
  const [buildStep, setBuildStep] = useState(0)
  const [svcFilter, setSvcFilter] = useState('')

  // ---- plugin wizard options (PLUGIN_OPTS / pluginChoices / pluginOptsFw) ----
  const [pluginOpts, setPluginOpts] = useState<OptionSchema[]>([])
  const [pluginChoices, setPluginChoices] = useState<Record<string, string[]>>({})
  const pluginOptsFw = useRef<string | null>(null)

  // ---- credentials + review state (CREDS_WANTED / CREDS / CREDS_EXTRA / name)-
  const [credsWanted, setCredsWanted] = useState<CredentialDTO[]>([])
  const [envNames, setEnvNames] = useState<string[]>([])
  const [creds, setCreds] = useState<Record<string, { username?: string; secret?: string; remember?: boolean }>>({})
  const [credsExtra, setCredsExtra] = useState<ExtraCred[]>([])
  const [pname, setPname] = useState('')

  // ---- live plan panel state (resolve()) ------------------------------------
  const [plan, setPlan] = useState<ResolveResult | null>(null)

  // ---- pure catalogue helpers (ported verbatim) -----------------------------
  const eng = useMemo(() => {
    const applies = (r: Recipe, fw: string | null) =>
      !r.appliesTo || !r.appliesTo.length || (!!fw && r.appliesTo.includes(fw)) || r.appliesTo.includes('*')
    const isDefault = (r: Recipe, fw: string | null) =>
      r.defaultFor && r.defaultFor.length ? (!!fw && r.defaultFor.includes(fw)) || r.defaultFor.includes('*') : !!r.default
    const ofKind = (k: string) => R.filter((r) => r.kind === k)
    const frameworksFor = (l: string | null) => R.filter((r) => r.kind === 'framework' && (r.lang || 'other') === l)
    const byId = (id: string) => R.find((x) => x.id === id)
    const isWeb = (r: Recipe | undefined) => !!r && (r.provides || []).includes('webserver')
    const forFw = (fw: string | null, k: string) => ofKind(k).filter((r) => applies(r, fw))
    const webFor = (fw: string | null) => forFw(fw, 'service').filter(isWeb)
    const svcFor = (fw: string | null) => forFw(fw, 'service').filter((r) => !isWeb(r))
    // families groups a language's frameworks by family, each with its variants
    // and a primary (the one whose id is the family key, else the first).
    type Family = { key: string; variants: Recipe[]; primary: Recipe }
    const families = (l: string | null): Family[] => {
      const seen: Record<string, { key: string; variants: Recipe[] }> = {}
      const out: { key: string; variants: Recipe[] }[] = []
      frameworksFor(l).forEach((f) => {
        const key = f.family || f.id
        if (!seen[key]) {
          seen[key] = { key, variants: [] }
          out.push(seen[key])
        }
        seen[key].variants.push(f)
      })
      return out.map((F) => ({ ...F, primary: F.variants.find((v) => v.id === F.key) || F.variants[0] }))
    }
    // conflictsWith: two recipes exclude each other by capability — one's
    // conflicts meeting the other's provides.
    const conflictsWith = (a?: Recipe, b?: Recipe) => {
      if (!a || !b) return false
      const ap = a.provides || [],
        ac = a.conflicts || [],
        bp = b.provides || [],
        bc = b.conflicts || []
      return ac.some((c) => bp.includes(c)) || bc.some((c) => ap.includes(c))
    }
    return { applies, isDefault, ofKind, frameworksFor, byId, isWeb, forFw, webFor, svcFor, families, conflictsWith }
  }, [R])

  const chosenWeb = (s: Sel) => s.service.find((id) => eng.isWeb(eng.byId(id))) || null
  // envProvidesDb: the db an env brings up itself (its db-<name> provide). When
  // set, the Database step is skipped (its alternatives conflict with the env's).
  const envProvidesDb = (s: Sel) => {
    const e = s.env ? eng.byId(s.env) : undefined
    if (!e) return ''
    const cap = (e.provides || []).find((p) => p.indexOf('db-') === 0)
    return cap ? cap.slice(3) : ''
  }

  const csv = (v?: string) => (v || '').split(',').map((x) => x.trim()).filter(Boolean)

  // resetDeps + defaults, refactored to return a fresh sel (React state) rather
  // than mutating in place. defaults() seeds the framework's default recipes and
  // then overrides from the saved profile when it matches the framework — exactly
  // the original order (recipe defaults first, profile last).
  const seedDefaults = (base: Sel, fw: string): Sel => {
    const s: Sel = { ...base, env: null, db: null, frontend: null, service: [], addon: [], extra: [] }
    const setWeb = (id: string | null) => {
      s.service = s.service.filter((x) => !eng.isWeb(eng.byId(x)))
      if (id) s.service.push(id)
    }
    const e = eng.forFw(fw, 'env').find((r) => eng.isDefault(r, fw)) || eng.forFw(fw, 'env')[0]
    if (e) s.env = e.id
    const d = eng.forFw(fw, 'db').find((r) => eng.isDefault(r, fw))
    if (d) s.db = d.id
    ;(['service', 'addon', 'extra'] as const).forEach((k) => {
      s[k] = eng.forFw(fw, k).filter((r) => eng.isDefault(r, fw)).map((r) => r.id)
    })
    if (PROFILE && PROFILE.framework === fw) {
      const has = (k: string, id: string) => eng.forFw(fw, k).some((r) => r.id === id)
      if (PROFILE.env && has('env', PROFILE.env)) s.env = PROFILE.env
      if (PROFILE.database && has('db', PROFILE.database)) s.db = PROFILE.database
      if (PROFILE.frontend) s.frontend = has('frontend', PROFILE.frontend) ? PROFILE.frontend : s.frontend
      const v = (k: string, str: string) => csv(str).filter((id) => has(k, id))
      const legacyWeb = v('service', PROFILE.services || '').find((id) => eng.isWeb(eng.byId(id))) || null
      const seededWeb = chosenWeb(s)
      if (PROFILE.services) s.service = v('service', PROFILE.services).filter((id) => !eng.isWeb(eng.byId(id)))
      const savedWeb = (PROFILE.webserver || '').trim()
      if (savedWeb === 'none') setWeb(null)
      else if (savedWeb && has('service', savedWeb)) setWeb(savedWeb)
      else setWeb(legacyWeb || seededWeb || null)
      if (PROFILE.addons) s.addon = v('addon', PROFILE.addons)
      if (PROFILE.extras) s.extra = v('extra', PROFILE.extras)
    }
    return s
  }

  // ids() — the recipe ids the current sel resolves to (framework + backing).
  const ids = (s: Sel) =>
    [s.framework, s.env, s.db, s.frontend, ...s.service, ...s.addon, ...s.extra].filter(Boolean) as string[]

  // collectCreds — the values the build posts (mirrors collectCreds).
  const collectCreds = (): CredValue[] => {
    const out: CredValue[] = []
    credsWanted.forEach((c) => {
      const vv = creds[c.id] || {}
      if (!vv.secret) return
      out.push({ id: c.id, kind: c.kind, auth: c.auth || '', username: vv.username || '', secret: vv.secret, remember: !!vv.remember })
    })
    credsExtra.forEach((r) => {
      if (!r.id || !r.secret) return
      out.push({ id: r.id, kind: r.kind, username: r.username || '', secret: r.secret, remember: !!r.remember })
    })
    return out
  }

  // ---- buildSteps: the ordered step list for the current selection ----------
  const buildSteps = (s: Sel): Step[] => {
    const steps: Step[] = [
      { key: 'lang', title: 'Language' },
      { key: 'framework', title: 'Framework' },
    ]
    const fw = s.framework
    if (fw) {
      steps.push({ key: 'env', title: 'Local dev environment', kind: 'env' })
      if (eng.webFor(fw).length) steps.push({ key: 'web', title: 'Web server', kind: 'web' })
      // Skip Database when the env already brings one (its alternatives conflict).
      if (eng.forFw(fw, 'db').length && !envProvidesDb(s)) steps.push({ key: 'db', title: 'Database', kind: 'db' })
      if (eng.forFw(fw, 'frontend').length) steps.push({ key: 'frontend', title: 'Frontend', kind: 'frontend', optional: true })
      if (eng.svcFor(fw).length) steps.push({ key: 'service', title: 'Services', kind: 'service', multi: true })
      if (eng.forFw(fw, 'addon').length) steps.push({ key: 'addon', title: 'Add-ons', kind: 'addon', multi: true })
      if (eng.forFw(fw, 'extra').length) steps.push({ key: 'extra', title: 'Extras', kind: 'extra', multi: true })
      if (pluginOpts.length) steps.push({ key: 'plugopts', title: 'AI & assistants', kind: 'plugopts' })
      steps.push({ key: 'review', title: 'Review & build' })
    }
    return steps
  }

  // buildStepChosen — whether a step's requirement is met.
  const buildStepChosen = (st: Step): boolean => {
    if (st.key === 'lang') return !!sel.lang
    if (st.key === 'framework') return !!sel.framework
    if (st.key === 'web') return true
    if (st.key === 'plugopts' || st.key === 'review') return true
    if (st.multi || st.optional) return true
    return !!(sel as unknown as Record<string, unknown>)[st.key]
  }

  // buildStepSummary — the one-line collapsed summary on a done accordion.
  const nameOf = (id: string) => {
    const r = eng.byId(id)
    return r ? r.label || r.id : id
  }
  const buildStepSummary = (st: Step): string => {
    if (st.key === 'lang') return sel.lang ? LANG_LABEL[sel.lang] || sel.lang : ''
    if (st.key === 'framework') return sel.framework ? nameOf(sel.framework) : ''
    if (st.key === 'env') return sel.env ? nameOf(sel.env) : ''
    if (st.key === 'db') return sel.db ? nameOf(sel.db) : ''
    if (st.key === 'frontend') return sel.frontend ? nameOf(sel.frontend) : 'none'
    if (st.key === 'web') {
      const wsel = chosenWeb(sel)
      return wsel ? nameOf(wsel) : 'none'
    }
    if (st.multi) {
      const a = ((sel as unknown as Record<string, string[]>)[st.key] || []) as string[]
      return a.length ? a.map((id) => nameOf(id)).join(', ') : 'none'
    }
    if (st.key === 'plugopts') {
      const n = Object.values(pluginChoices).reduce((acc, a) => acc + (a ? a.length : 0), 0)
      return n ? n + ' selected' : 'defaults'
    }
    return ''
  }

  // ---- loadPluginOpts: fetch the applicable plugins' wizard options for the
  // chosen framework and seed each step's selection with its defaults. Refetched
  // only when the framework changes (pluginOptsFw guard). ----------------------
  const loadPluginOpts = async (fw: string | null) => {
    if (!fw) {
      setPluginOpts([])
      setPluginChoices({})
      pluginOptsFw.current = null
      return
    }
    if (pluginOptsFw.current === fw) return
    pluginOptsFw.current = fw
    let schemas: OptionSchema[] = []
    try {
      const d = await apiJSON<{ schemas?: OptionSchema[] }>('/api/plugin-options?framework=' + encodeURIComponent(fw))
      schemas = (d && d.schemas) || []
    } catch {
      schemas = []
    }
    // A late reply for a framework the user has since changed away from is stale.
    if (pluginOptsFw.current !== fw) return
    const choices: Record<string, string[]> = {}
    schemas.forEach((s) => {
      choices[s.id] = (s.choices || []).filter((o) => o.default).map((o) => o.value)
    })
    setPluginOpts(schemas)
    setPluginChoices(choices)
  }

  // ---- selection actions (chooseSingle / chooseFramily / chooseVariant /
  // chooseWeb / toggleBuild), each producing a fresh sel + triggering resolve. --
  const commit = (next: Sel) => setSel(next)

  const chooseSingle = (key: keyof Sel, val: string | null) => {
    setSel((prev) => {
      let s: Sel = { ...prev, [key]: val } as Sel
      if (key === 'lang') {
        // A new language clears the framework and every dependent choice
        // (sel.framework=null; resetDeps()), and drops the plugin options.
        s = { ...s, framework: null, env: null, db: null, frontend: null, service: [], addon: [], extra: [] }
        setPluginOpts([])
        pluginOptsFw.current = null
      }
      if (key === 'framework') {
        s = seedDefaults(s, val as string)
        loadPluginOpts(val as string)
      }
      if (key === 'env' && envProvidesDb(s)) s.db = null
      return s
    })
  }

  const chooseFramilyByKey = (key: string) => {
    const F = eng.families(sel.lang).find((x) => x.key === key)
    if (!F) return
    if (F.variants.length === 1) {
      chooseSingle('framework', F.primary.id)
      return
    }
    const s = seedDefaults({ ...sel, framework: F.primary.id }, F.primary.id)
    commit(s)
    loadPluginOpts(F.primary.id)
  }
  const chooseVariant = (id: string) => {
    const s = seedDefaults({ ...sel, framework: id }, id)
    commit(s)
    loadPluginOpts(id)
  }
  const chooseWeb = (id: string | null) => {
    setSel((prev) => {
      const s: Sel = { ...prev, service: prev.service.filter((x) => !eng.isWeb(eng.byId(x))) }
      if (id) s.service = [...s.service, id]
      return s
    })
    // chooseWeb advances a step in the original.
    setBuildStep((i) => i + 1)
  }

  const toggle = (s: Sel, k: 'service' | 'addon' | 'extra', id: string): Sel => {
    const arr = s[k]
    const i = arr.indexOf(id)
    return { ...s, [k]: i >= 0 ? arr.filter((x) => x !== id) : [...arr, id] }
  }
  const toggleBuild = (k: 'service' | 'addon' | 'extra', id: string) => {
    setSel((prev) => {
      const arr = prev[k]
      if (k === 'service' && arr.indexOf(id) < 0) {
        const s = eng.byId(id)
        const clash = arr.map((x) => eng.byId(x)).find((o) => eng.conflictsWith(s, o))
        if (clash) {
          // Hard conflict (Elasticsearch/OpenSearch share a port): swap, not stack.
          let next = toggle(prev, k, clash.id)
          next = toggle(next, k, id)
          con.toast(
            `Only one ${clash.category || 'of these'} at a time: replaced ${clash.label} with ${s?.label}.`,
            { kind: 'info', id: 'svcSwap' },
          )
          return next
        }
        // Soft warning: a second search engine — allowed but flagged.
        for (const role of ['search']) {
          if ((s?.provides || []).includes(role)) {
            const other = arr.map((x) => eng.byId(x)).find((o) => o && (o.provides || []).includes(role))
            if (other)
              con.toast(`You already added ${other.label}. Most projects need one ${role} engine, not two.`, {
                kind: 'info',
                id: 'svcDup',
              })
          }
        }
      }
      return toggle(prev, k, id)
    })
  }

  // ---- plugin-choice actions -------------------------------------------------
  const togglePluginChoice = (id: string, val: string) =>
    setPluginChoices((prev) => {
      const a = prev[id] || []
      const i = a.indexOf(val)
      return { ...prev, [id]: i >= 0 ? a.filter((x) => x !== val) : [...a, val] }
    })
  const pickPluginChoice = (id: string, val: string) => setPluginChoices((prev) => ({ ...prev, [id]: [val] }))

  // ---- resolve(): rebuild the live plan panel whenever the selection changes.
  // A conflict comes back as {error} — shown in the panel AND (for the
  // recognisable conflict phrasings) raised as a toast, exactly as the original.
  useEffect(() => {
    let cancelled = false
    if (!sel.framework) {
      setPlan(null)
      return
    }
    const run = async () => {
      const d = await fetchJSON<ResolveResult>('/api/resolve', { ids: ids(sel) })
      if (cancelled) return
      if ('error' in d && d.error !== undefined) {
        // credentials/envNames may still ride along on a non-error resolve; an
        // error path carries none, so leave the wanted list as-is.
        setPlan(d as ResolveResult)
        if (/cannot be used|conflict|already brings|already provides|incompatible/i.test(d.error))
          con.toast(d.error, { kind: 'bad', id: 'buildConflict' })
        return
      }
      const r = d as ResolveResult
      if (r.credentials !== undefined) {
        setCredsWanted(r.credentials || [])
        setEnvNames(r.envNames || [])
      }
      setPlan(r)
    }
    run()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    sel.framework,
    sel.env,
    sel.db,
    sel.frontend,
    sel.service.join(','),
    sel.addon.join(','),
    sel.extra.join(','),
  ])

  // ---- build(): stream the plan through the console, with the untrusted-pack
  // consent handshake. On consent, confirm the hooks, then re-stream with the
  // grant. On success the console invalidates queries; navigate home (the studio
  // tracked the new project and returned to the workspace). --------------------
  const build = async (run: boolean) => {
    const name = pname || ''
    const options = { ...pluginChoices }
    const credentials = collectCreds()
    const consent = (await con.stream('/api/build', { ids: ids(sel), name, run, credentials, options })) as
      | { grant?: string; steps?: string[] }
      | null
    if (!consent) return
    const hooks = (consent.steps || []).map((s) => '  → ' + s).join('\n')
    if (
      !window.confirm(
        'This plan includes recipes from an untrusted pack.\n\nBuilding it will run:\n' +
          (hooks || '  (no hooks)') +
          '\n\nRun these untrusted recipes?',
      )
    )
      return
    await con.stream('/api/build', { ids: ids(sel), name, run, credentials, options, consent: consent.grant })
  }

  // ---- render ---------------------------------------------------------------
  const steps = buildSteps(sel)
  const clampedStep = Math.min(buildStep, steps.length - 1)
  let firstUnmet = steps.findIndex((s) => !buildStepChosen(s))
  if (firstUnmet < 0) firstUnmet = steps.length - 1

  const goStep = (i: number) => setBuildStep(Math.max(0, Math.min(i, steps.length - 1)))

  return (
    <>
      <h1 className="page">Build a stack</h1>
      <p className="lede">
        Compose a new project step by step: keel wires the env, database, services and add-ons, then runs the real
        installers. Each step opens in turn; earlier ones collapse to a summary, later ones unlock as their
        prerequisites are met. The plan on the right updates live.
      </p>
      <div className="wrap2" style={{ height: 'calc(100% - 84px)' }}>
        <div className="colL" id="tree">
          {steps.map((st, i) => {
            let state: 'open' | 'done' | 'locked'
            if (i === clampedStep) state = 'open'
            else if (i > firstUnmet) state = 'locked'
            else state = buildStepChosen(st) ? 'done' : 'open'
            const summary =
              state === 'done' ? buildStepSummary(st) : state === 'locked' ? 'complete the step above first' : ''
            return (
              <Accordion
                key={st.key}
                n={i + 1}
                title={st.title}
                summary={summary}
                state={state}
                onToggle={() => goStep(i)}
              >
                {state === 'open' ? (
                  <StepBody
                    st={st}
                    sel={sel}
                    eng={eng}
                    chosenWeb={chosenWeb}
                    svcFilter={svcFilter}
                    setSvcFilter={setSvcFilter}
                    LANGS={LANGS}
                    pluginOpts={pluginOpts}
                    pluginChoices={pluginChoices}
                    credsWanted={credsWanted}
                    envNames={envNames}
                    creds={creds}
                    setCreds={setCreds}
                    credsExtra={credsExtra}
                    setCredsExtra={setCredsExtra}
                    pname={pname}
                    setPname={setPname}
                    onChooseSingle={chooseSingle}
                    onChooseFramily={chooseFramilyByKey}
                    onChooseVariant={chooseVariant}
                    onChooseWeb={chooseWeb}
                    onToggleBuild={toggleBuild}
                    onTogglePluginChoice={togglePluginChoice}
                    onPickPluginChoice={pickPluginChoice}
                    onBuild={build}
                  />
                ) : null}
              </Accordion>
            )
          })}
          <NavBar
            step={clampedStep}
            steps={steps}
            chosen={buildStepChosen}
            onStep={goStep}
          />
        </div>
        <div className="colR" id="plan">
          <PlanPanel sel={sel} plan={plan} />
        </div>
      </div>
    </>
  )
}

// ---- the floating Back/Next bar (sticky at the column bottom) ----------------
function NavBar({
  step,
  steps,
  chosen,
  onStep,
}: {
  step: number
  steps: Step[]
  chosen: (st: Step) => boolean
  onStep: (i: number) => void
}) {
  const st = steps[step]
  return (
    <div
      className="row"
      style={{
        position: 'sticky',
        bottom: 14,
        marginTop: 'var(--s4)',
        padding: '11px 14px',
        gap: 10,
        background: 'var(--panel)',
        border: '1px solid var(--line2)',
        borderRadius: 12,
        boxShadow: '0 10px 28px -10px rgba(0,0,0,.7)',
      }}
    >
      <button className="btn ghost" disabled={step === 0} onClick={() => onStep(step - 1)}>
        ← Back
      </button>
      {st && st.key !== 'review' && (
        <button className="btn" disabled={!chosen(st)} onClick={() => onStep(step + 1)}>
          Next →
        </button>
      )}
    </div>
  )
}

// ---- the live plan panel (resolve() output + service graph) ------------------
function PlanPanel({ sel, plan }: { sel: Sel; plan: ResolveResult | null }) {
  if (!sel.framework) {
    return (
      <div style={{ textAlign: 'center', paddingTop: 16 }}>
        <div className="crabpen">
          <img
            className="crab"
            src="/assets/crab.png"
            alt="keel crab"
            onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')}
          />
        </div>
        <div className="muted">Pick a framework and keel builds the plan.</div>
      </div>
    )
  }
  if (!plan) return null
  if (plan.error) {
    return (
      <>
        <h2>Plan</h2>
        <div className="err">{plan.error}</div>
      </>
    )
  }
  const recipes = plan.recipes || []
  const steps = (plan.steps || []).map((s) => '$ ' + s).join('\n')
  return (
    <>
      <h2>Plan · {plan.framework}</h2>
      <ServiceGraph recipes={recipes} />
      <h3 style={{ marginTop: 6 }}>Recipes</h3>
      {recipes.map((r, i) => (
        <div className="plan-row" key={i}>
          <div className="k">{r.kind}</div>
          <div>{r.label}</div>
        </div>
      ))}
      <h3 style={{ marginTop: 20 }}>Steps, in order</h3>
      <pre>{steps || '(none)'}</pre>
    </>
  )
}

// serviceGraph — app → backing services → frontend, ported verbatim.
function GNode({ r, cls }: { r: PlanRecipe; cls?: string }) {
  return (
    <div className={'gnode ' + (cls || '')}>
      <Icon r={r} size={20} />
      <div>
        <b>{(r.label || r.id).split(' (')[0]}</b>
        <small>{r.kind}</small>
      </div>
    </div>
  )
}
function ServiceGraph({ recipes }: { recipes: PlanRecipe[] }) {
  const app = recipes.find((r) => r.kind === 'framework')
  const backing = recipes.filter((r) => r.kind === 'db' || r.kind === 'service')
  const fe = recipes.find((r) => r.kind === 'frontend')
  if (!app) return null
  return (
    <div className="graph">
      <div className="grow-row">
        <GNode r={app} cls="app" />
      </div>
      {backing.length > 0 && (
        <>
          <div className="gconn" />
          <div className="grow-row">
            {backing.map((n, i) => (
              <GNode key={i} r={n} />
            ))}
          </div>
        </>
      )}
      {fe && (
        <>
          <div className="gconn" />
          <div className="grow-row">
            <GNode r={fe} cls="fe" />
          </div>
        </>
      )}
    </div>
  )
}

// ---- the open step body (buildStepBody + reviewStep/credentialsStep/
// pluginOptsStep) -------------------------------------------------------------
// Eng is the shape of the memoised catalogue-helper bundle StepBody consumes.
type Eng = {
  applies: (r: Recipe, fw: string | null) => boolean
  isDefault: (r: Recipe, fw: string | null) => boolean
  ofKind: (k: string) => Recipe[]
  frameworksFor: (l: string | null) => Recipe[]
  byId: (id: string) => Recipe | undefined
  isWeb: (r: Recipe | undefined) => boolean
  forFw: (fw: string | null, k: string) => Recipe[]
  webFor: (fw: string | null) => Recipe[]
  svcFor: (fw: string | null) => Recipe[]
  families: (l: string | null) => { key: string; variants: Recipe[]; primary: Recipe }[]
  conflictsWith: (a?: Recipe, b?: Recipe) => boolean
}

function StepBody(props: {
  st: Step
  sel: Sel
  eng: Eng
  chosenWeb: (s: Sel) => string | null
  svcFilter: string
  setSvcFilter: (v: string) => void
  LANGS: string[]
  pluginOpts: OptionSchema[]
  pluginChoices: Record<string, string[]>
  credsWanted: CredentialDTO[]
  envNames: string[]
  creds: Record<string, { username?: string; secret?: string; remember?: boolean }>
  setCreds: React.Dispatch<React.SetStateAction<Record<string, { username?: string; secret?: string; remember?: boolean }>>>
  credsExtra: ExtraCred[]
  setCredsExtra: React.Dispatch<React.SetStateAction<ExtraCred[]>>
  pname: string
  setPname: (v: string) => void
  onChooseSingle: (key: keyof Sel, val: string | null) => void
  onChooseFramily: (key: string) => void
  onChooseVariant: (id: string) => void
  onChooseWeb: (id: string | null) => void
  onToggleBuild: (k: 'service' | 'addon' | 'extra', id: string) => void
  onTogglePluginChoice: (id: string, val: string) => void
  onPickPluginChoice: (id: string, val: string) => void
  onBuild: (run: boolean) => void
}) {
  const { st, sel, eng } = props
  const fw = sel.framework

  if (st.key === 'lang') {
    return (
      <RList
        items={props.LANGS.map((l) => ({
          on: sel.lang === l,
          label: LANG_LABEL[l] || l,
          slug: iconSlug(l),
          onClick: () => props.onChooseSingle('lang', l),
        }))}
      />
    )
  }

  if (st.key === 'framework') {
    const fams = eng.families(sel.lang)
    const cur = fams.find((F) => F.variants.some((v) => v.id === sel.framework))
    return (
      <>
        <RList
          items={fams.map((F) => ({
            on: F.variants.some((v) => v.id === sel.framework),
            label: F.primary.label,
            slug: iconSlug(F.primary),
            onClick: () => props.onChooseFramily(F.key),
          }))}
        />
        {cur && cur.variants.length > 1 && (
          <>
            <div className="msub">Framework type</div>
            <RList
              items={cur.variants.map((v) => ({
                on: sel.framework === v.id,
                label: v.variant || v.label,
                slug: iconSlug(v),
                onClick: () => props.onChooseVariant(v.id),
              }))}
            />
          </>
        )}
      </>
    )
  }

  if (st.key === 'web') {
    const items = eng.webFor(fw).map((e) => ({
      on: props.chosenWeb(sel) === e.id,
      label: e.label,
      slug: iconSlug(e),
      onClick: () => props.onChooseWeb(e.id),
    }))
    items.push({ on: !props.chosenWeb(sel), label: "None (I'll front it myself)", slug: '', onClick: () => props.onChooseWeb(null) })
    return <RList items={items} />
  }

  if (st.multi) {
    const items = st.kind === 'service' ? eng.svcFor(fw) : eng.forFw(fw, st.kind!)
    const selArr = (sel as unknown as Record<string, string[]>)[st.key] || []
    const q = props.svcFilter.trim().toLowerCase()
    const matches = (e: Recipe) => !q || (e.label + ' ' + (e.category || '')).toLowerCase().includes(q)
    const mk = (e: Recipe) => (
      <MItem
        key={e.id}
        on={selArr.includes(e.id)}
        label={e.label}
        slug={iconSlug(e)}
        onClick={() => props.onToggleBuild(st.key as 'service' | 'addon' | 'extra', e.id)}
      />
    )
    if (items.some((e) => e.category)) {
      const cats: Record<string, Recipe[]> = {}
      items.forEach((e) => {
        const k = e.category || 'Other'
        ;(cats[k] = cats[k] || []).push(e)
      })
      return (
        <>
          <input
            className="mfilter"
            placeholder={`Filter ${st.title.toLowerCase()}…  (try: search, cache, queue)`}
            value={props.svcFilter}
            onChange={(e) => props.setSvcFilter(e.target.value)}
          />
          <div className="mlist" id="buildmlist">
            {Object.keys(cats)
              .sort()
              .map((cat) => {
                const shown = cats[cat].filter(matches)
                if (!shown.length) return null
                return (
                  <div key={cat} style={{ display: 'contents' }}>
                    <div className="msub">{cat}</div>
                    {shown.map(mk)}
                  </div>
                )
              })}
          </div>
        </>
      )
    }
    return (
      <div className="mlist" id="buildmlist">
        {items.map(mk)}
      </div>
    )
  }

  if (st.optional) {
    const items = [
      { on: !(sel as unknown as Record<string, unknown>)[st.key], label: 'None', slug: '', onClick: () => props.onChooseSingle(st.key as keyof Sel, null) },
    ].concat(
      eng.forFw(fw, st.kind!).map((e) => ({
        on: (sel as unknown as Record<string, unknown>)[st.key] === e.id,
        label: e.label,
        slug: iconSlug(e),
        onClick: () => props.onChooseSingle(st.key as keyof Sel, e.id),
      })),
    )
    return <RList items={items} />
  }

  if (st.key === 'plugopts') {
    return (
      <PluginOptsStep
        pluginOpts={props.pluginOpts}
        pluginChoices={props.pluginChoices}
        onToggle={props.onTogglePluginChoice}
        onPick={props.onPickPluginChoice}
      />
    )
  }

  if (st.key === 'review') {
    return (
      <ReviewStep
        sel={sel}
        credsWanted={props.credsWanted}
        envNames={props.envNames}
        creds={props.creds}
        setCreds={props.setCreds}
        credsExtra={props.credsExtra}
        setCredsExtra={props.setCredsExtra}
        pname={props.pname}
        setPname={props.setPname}
        onBuild={props.onBuild}
      />
    )
  }

  // plain single-select (env, db)
  return (
    <RList
      items={eng.forFw(fw, st.kind!).map((e) => ({
        on: (sel as unknown as Record<string, unknown>)[st.key] === e.id,
        label: e.label,
        slug: iconSlug(e),
        onClick: () => props.onChooseSingle(st.key as keyof Sel, e.id),
      }))}
    />
  )
}

// ---- pluginOptsStep: the applicable plugins' wizard options -----------------
function PluginOptsStep({
  pluginOpts,
  pluginChoices,
  onToggle,
  onPick,
}: {
  pluginOpts: OptionSchema[]
  pluginChoices: Record<string, string[]>
  onToggle: (id: string, val: string) => void
  onPick: (id: string, val: string) => void
}) {
  if (!pluginOpts.length) return <p className="muted">No plugin has options for this stack.</p>
  return (
    <>
      <p className="muted" style={{ fontSize: 'var(--t-sm)', marginTop: 0 }}>
        These come from the plugins that apply to your stack. The same questions <code>keel new</code> asks. Pick what
        you want; unchecked items are simply not installed.
      </p>
      {pluginOpts.map((s) => {
        const chosen = pluginChoices[s.id] || []
        return (
          <div key={s.id} style={{ display: 'contents' }}>
            <div className="msub">{s.label || s.id}</div>
            {s.help && (
              <div className="muted" style={{ fontSize: 12, margin: '-4px 0 8px' }}>
                {s.help}
              </div>
            )}
            {s.type === 'multi' ? (
              <div className="mlist">
                {(s.choices || []).map((o) => (
                  <MItem
                    key={o.value}
                    on={chosen.includes(o.value)}
                    label={o.label || o.value}
                    sub={o.description || undefined}
                    onClick={() => onToggle(s.id, o.value)}
                  />
                ))}
              </div>
            ) : (
              <RList
                items={(s.choices || []).map((o) => ({
                  on: chosen.includes(o.value),
                  label: o.label || o.value,
                  sub: o.description || undefined,
                  onClick: () => onPick(s.id, o.value),
                }))}
              />
            )}
          </div>
        )
      })}
    </>
  )
}

// ---- reviewStep = credentialsStep + project name + build buttons ------------
function ReviewStep({
  sel,
  credsWanted,
  envNames,
  creds,
  setCreds,
  credsExtra,
  setCredsExtra,
  pname,
  setPname,
  onBuild,
}: {
  sel: Sel
  credsWanted: CredentialDTO[]
  envNames: string[]
  creds: Record<string, { username?: string; secret?: string; remember?: boolean }>
  setCreds: React.Dispatch<React.SetStateAction<Record<string, { username?: string; secret?: string; remember?: boolean }>>>
  credsExtra: ExtraCred[]
  setCredsExtra: React.Dispatch<React.SetStateAction<ExtraCred[]>>
  pname: string
  setPname: (v: string) => void
  onBuild: (run: boolean) => void
}) {
  const setCred = (id: string, field: 'username' | 'secret' | 'remember', val: string | boolean) =>
    setCreds((prev) => ({ ...prev, [id]: { ...prev[id], [field]: val } }))
  const setExtra = (i: number, field: keyof ExtraCred, val: string | boolean) =>
    setCredsExtra((prev) => prev.map((r, j) => (j === i ? { ...r, [field]: val } : r)))
  const addCredRow = (kind: string) =>
    setCredsExtra((prev) => [...prev, { kind, id: '', username: '', secret: '', remember: false }])
  const removeCredRow = (i: number) => setCredsExtra((prev) => prev.filter((_, j) => j !== i))

  return (
    <div>
      {/* credentialsStep */}
      <div>
        <h3>Credentials</h3>
        {!credsWanted.length && !credsExtra.length && (
          <p className="muted" style={{ fontSize: 12.5 }}>
            This stack needs none. You can still add API keys for the project's .env.
          </p>
        )}
        {credsWanted.map((c) => {
          const bearer = c.kind === 'env' || c.auth === 'bearer'
          const v = creds[c.id] || {}
          return (
            <div key={c.id} style={{ marginBottom: 14 }}>
              <div className="row" style={{ gap: 8 }}>
                <b>{c.label || c.id}</b>
                {c.required ? (
                  <span className="tag" style={{ color: 'var(--orange)', borderColor: 'var(--orange)' }}>
                    required
                  </span>
                ) : (
                  <span className="tag">optional</span>
                )}
                {c.saved && <span className="tag ok">remembered</span>}
              </div>
              <div className="muted" style={{ fontSize: 11.5 }}>
                {c.id}
              </div>
              {c.help && (
                <div className="muted" style={{ fontSize: 12, margin: '4px 0 8px' }}>
                  <Linkify text={c.help} />
                </div>
              )}
              {bearer ? (
                <input
                  type="password"
                  placeholder={c.kind === 'env' ? 'value' : 'token'}
                  onInput={(e) => setCred(c.id, 'secret', (e.target as HTMLInputElement).value)}
                />
              ) : (
                <div className="row">
                  <input
                    placeholder="username / public key"
                    onInput={(e) => setCred(c.id, 'username', (e.target as HTMLInputElement).value)}
                  />
                  <input
                    type="password"
                    placeholder="password / private key"
                    onInput={(e) => setCred(c.id, 'secret', (e.target as HTMLInputElement).value)}
                  />
                </div>
              )}
              <div className="chk" style={{ marginTop: 6 }}>
                <input
                  type="checkbox"
                  checked={!!v.remember}
                  onChange={(e) => setCred(c.id, 'remember', e.target.checked)}
                />{' '}
                Remember for future projects
              </div>
            </div>
          )
        })}
        {/* credextra */}
        {credsExtra.map((row, i) => (
          <div key={i} style={{ margin: '12px 0' }}>
            <div className="row" style={{ gap: 8 }}>
              <span className="tag">{row.kind === 'env' ? 'env' : 'composer'}</span>
              {row.kind === 'env' ? (
                <input
                  list="envnames"
                  placeholder="GOOGLE_ANALYTICS_ID"
                  value={row.id}
                  onInput={(e) => setExtra(i, 'id', (e.target as HTMLInputElement).value)}
                />
              ) : (
                <input
                  placeholder="repo.amasty.com"
                  value={row.id}
                  onInput={(e) => setExtra(i, 'id', (e.target as HTMLInputElement).value)}
                />
              )}
              <button className="btn ghost sm" onClick={() => removeCredRow(i)}>
                remove
              </button>
            </div>
            <div className="row" style={{ marginTop: 6 }}>
              {row.kind === 'env' ? (
                <input
                  type="password"
                  placeholder="value"
                  onInput={(e) => setExtra(i, 'secret', (e.target as HTMLInputElement).value)}
                />
              ) : (
                <>
                  <input
                    placeholder="username / public key"
                    onInput={(e) => setExtra(i, 'username', (e.target as HTMLInputElement).value)}
                  />
                  <input
                    type="password"
                    placeholder="password / private key"
                    onInput={(e) => setExtra(i, 'secret', (e.target as HTMLInputElement).value)}
                  />
                </>
              )}
            </div>
            <div className="chk" style={{ marginTop: 6 }}>
              <input type="checkbox" onChange={(e) => setExtra(i, 'remember', e.target.checked)} /> Remember for future
              projects
            </div>
          </div>
        ))}
        <datalist id="envnames">
          {envNames.map((n) => (
            <option key={n} value={n} />
          ))}
        </datalist>
        <div className="row" style={{ marginTop: 10 }}>
          <button className="btn sm" onClick={() => addCredRow('env')}>
            + API key (.env)
          </button>
          <button className="btn sm" onClick={() => addCredRow('composer')}>
            + Private Composer repo
          </button>
        </div>
        <p className="muted" style={{ fontSize: 12, marginTop: 12 }}>
          Written straight to auth.json and .env with owner-only permissions, wherever this environment reads them. Never
          shown in the console below.
        </p>
      </div>
      {/* project name + build buttons */}
      <div>
        <h3 style={{ marginTop: 22 }}>Project name</h3>
        <input
          id="pname"
          placeholder={(sel.framework || 'app') + '-app'}
          value={pname}
          onInput={(e) => setPname((e.target as HTMLInputElement).value)}
        />
        <div className="row" style={{ marginTop: 16 }}>
          <button className="btn" onClick={() => onBuild(false)}>
            Preview (dry run)
          </button>
          <button className="btn primary" onClick={() => onBuild(true)}>
            Build for real
          </button>
        </div>
        <p className="muted" style={{ fontSize: 12, marginTop: 10 }}>
          Build runs the real installers and streams output in the console below. If the plan includes recipes from an
          untrusted pack, keel lists the hooks it would run and asks first.
        </p>
      </div>
    </div>
  )
}

// ---- shared primitives: accHTML / rlistHTML / mitemHTML as components --------

// Accordion is accHTML: numbered/checked badge, title + summary, chevron, and the
// grid-based collapse. open/done/locked — a locked header is not focusable and
// does not toggle, exactly as the original.
function Accordion({
  n,
  title,
  summary,
  state,
  onToggle,
  children,
}: {
  n: number
  title: string
  summary: string
  state: 'open' | 'done' | 'locked'
  onToggle: () => void
  children: ReactNode
}) {
  const open = state === 'open'
  const done = state === 'done'
  const locked = state === 'locked'
  const cls = 'acc' + (open ? ' open' : '') + (done ? ' done' : '') + (locked ? ' locked' : '')
  const badge = done ? '✓' : String(n)
  return (
    <div className={cls}>
      <div
        className="acc-h"
        tabIndex={locked ? -1 : 0}
        role="button"
        aria-expanded={open}
        onClick={locked ? undefined : onToggle}
        onKeyDown={
          locked
            ? undefined
            : (e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  onToggle()
                }
              }
        }
      >
        <span className="acc-badge">{badge}</span>
        <span className="acc-t">
          <b>{title}</b>
          {summary ? <small>{summary}</small> : null}
        </span>
        <span className="acc-chev">▸</span>
      </div>
      <div className="acc-body-wrap">
        <div className="acc-body-inner">
          <div className="acc-body">{children}</div>
        </div>
      </div>
    </div>
  )
}

// RList is rlistHTML: a single-select radio list with an optional brand icon.
type RItem = { on: boolean; label: string; sub?: string; slug?: string; onClick: () => void }
function RList({ items }: { items: RItem[] }) {
  return (
    <div className="rlist">
      {items.map((it, i) => (
        <div
          key={i}
          className={'ritem' + (it.on ? ' on' : '')}
          tabIndex={0}
          role="radio"
          aria-checked={it.on}
          onClick={it.onClick}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              it.onClick()
            }
          }}
        >
          <span className="dot" />
          <SlugIcon slug={it.slug} />
          <span className="rl">
            <b>{it.label}</b>
            {it.sub ? <small>{it.sub}</small> : null}
          </span>
        </div>
      ))}
    </div>
  )
}

// MItem is mitemHTML: one checkbox row of a multi-select.
function MItem({
  on,
  label,
  sub,
  slug,
  onClick,
}: {
  on: boolean
  label: string
  sub?: string
  slug?: string
  onClick: () => void
}) {
  return (
    <div
      className={'mitem' + (on ? ' on' : '')}
      tabIndex={0}
      role="checkbox"
      aria-checked={on}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onClick()
        }
      }}
    >
      <span className="box" />
      <SlugIcon slug={slug} />
      <span className="ml">
        <b>{label}</b>
        {sub ? <small>{sub}</small> : null}
      </span>
    </div>
  )
}
