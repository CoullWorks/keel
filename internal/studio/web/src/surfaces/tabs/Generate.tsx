import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchJSON } from '../../lib/api'
import { type Project, type Member, type Backend } from '../../lib/types'
import { useConsole } from '../../lib/console'

// Generate.tsx is the React port of the studio's per-project GENERATE tab
// (renderGenerate + renderGenAll + genHeaderHTML/genSectionsHTML/
// genComponentAccordion/genComponentBody/genStagedRow/genDraftForm/
// genScalarControl/genFieldsTable/genStackAccordion/genFooterHTML and the
// staging actions commitDraft/editStaged/dupStaged/removeStaged and
// submitGenPlan/addStack). It is a MULTI-STAGE code generator: keel asks the
// project's framework what it can generate (GET /api/generators), then the user
// stages one or more components (each with its own typed form + fields table)
// into a ModulePlan and hits Generate, which POSTs the whole plan to
// /api/generate (Magento renders the plan at once; every other framework
// re-execs `keel gen` per component — cosmetic to the UI).
//
// Only the framework's CATALOGUE differs; the flow is identical for every stack.
// A framework with a real module concept (Magento's Vendor_Module, NestJS's
// @Module) shows the "Module" header; Next/Laravel/Django/Symfony/FastAPI
// generate components directly, so there is no header and no required module
// name.
//
// Generate + stack-add stream through the shared console (con.stream), so the
// generated output renders live below. The staged-selection UI and all of its
// local state are ported faithfully.

// --- server shapes (see internal/studio/generate.go + generate_plan.go) ------

// A typed input a generator collects, in the studio's lowercase JSON shape. The
// server's genInputDTO only serializes name/type/label/required/choices, so
// default + help arrive undefined — the original read them defensively anyway,
// and this port keeps that (behaviour is identical because the data is absent).
type GenInput = {
  name: string
  type: string
  label?: string
  required?: boolean
  choices?: string[]
  default?: string
  help?: string
}
// One thing keel can generate into the focused project, grouped by level.
type Generatable = {
  key: string
  label: string
  level: string
  provides?: string[]
  inputs?: GenInput[]
  recipe?: string
  applies?: string[]
}
type GeneratorsResult = {
  framework?: string
  hasModule?: boolean
  generatables?: Generatable[]
  error?: string
}

// --- staged-plan state (the original GEN singleton, now local React state) ---

// One typed column in the shared fields table, mirroring GEN.draft.fields[i].
type Field = {
  name: string
  type: string
  nullable: boolean
  unique: boolean
  index: boolean
  default: string
  length: number
}
// A staged component instance backing gen.PlanComponent (GEN.plan.components[i]).
type Component = {
  type: string
  params: Record<string, unknown>
  fields: Field[]
}
// The instance being added/edited: a draft is a component + an editIdx (-1 for a
// brand-new add, >=0 when editing an existing staged instance in place).
type Draft = Component & { editIdx: number }
// The ModulePlan being staged.
type Plan = { vendor: string; module: string; target: string; components: Component[] }

// GEN_TYPES / LEVELS ported verbatim from index.html.
const GEN_TYPES = ['string', 'int', 'decimal', 'bool', 'text', 'datetime', 'foreignId', 'json']
const LEVELS: [string, string][] = [
  ['code-block', 'Code blocks'],
  ['module', 'Modules'],
  ['package', 'Packages'],
  ['stack', 'Stacks'],
]

// genWholePlan — a framework keel renders a whole plan for (Magento) vs one that
// falls back to per-component `keel gen`. Cosmetic only; the backend enforces the
// real path. Ported verbatim.
const genWholePlan = (fw: string) => fw === 'magento'

// targetLabel / targetHelp — the module target radio's labels, ported verbatim.
const targetLabel = (t: string) => (t === 'package' ? 'Composer package' : t === 'app-code' ? 'App code' : t)
const targetHelp = (t: string) =>
  t === 'package'
    ? 'a distributable package under vendor/'
    : t === 'app-code'
      ? 'drops it in the application tree (app/code)'
      : ''

// --- pure catalogue helpers (genModuleDef/genByKey/genInputsOf), ported -------
const genInputsOf = (g: Generatable | null | undefined): GenInput[] => (g && g.inputs) || []

// newGenDraft seeds a fresh draft for a component key: scalar inputs are seeded
// with their declared defaults so the form is honest (fields inputs are skipped).
// Ported verbatim from newGenDraft.
function newGenDraft(g: Generatable | null, key: string): Draft {
  const params: Record<string, unknown> = {}
  genInputsOf(g).forEach((i) => {
    if (i.type !== 'fields' && i.default !== undefined && i.default !== '') params[i.name] = i.default
  })
  return { type: key, params, fields: [], editIdx: -1 }
}

export default function GenerateTab({ p, be: _be }: { p: Project | Member; be: Backend | null }) {
  // GEN.dir — the target project directory; the plan is keyed to it. The original
  // reset the plan on every renderGenerate; here the plan state is component-local
  // and remounts (via key) when the project path changes, so switching focus
  // starts a clean plan exactly as before.
  return <GenerateSurface key={p.path} p={p} />
}

function GenerateSurface({ p }: { p: Project | Member }) {
  const dir = p.path

  // GET /api/generators?dir= — the catalogue for this project's framework. Keyed
  // by dir; a thrown fetch degrades to {error} (fetchJSON) so the panel renders
  // the calm error inline, exactly like the original `if(d.error)` branch.
  const q = useQuery({
    queryKey: ['generators', dir],
    queryFn: () => fetchJSON<GeneratorsResult>('/api/generators?dir=' + encodeURIComponent(dir)),
    retry: false,
  })

  const lede = (
    <p className="lede" style={{ marginTop: 0 }}>
      Generate code into <b>{p.name}</b>: add components (each with its own typed form and fields table), then Generate.
      Only this framework's catalogue differs; the flow is the same for every stack. Output streams below.
    </p>
  )

  // The initial "loading generators…" state the original paints into #genhost.
  if (q.isLoading || (!q.data && !q.isError)) {
    return (
      <>
        {lede}
        <div id="genhost">
          <span className="muted">loading generators for this stack…</span>
        </div>
      </>
    )
  }

  // A thrown fetch degrades to {error}; the original showed the raw driver error
  // in an .err block.
  const d: GeneratorsResult = q.isError
    ? { error: String((q.error as Error)?.message || q.error) }
    : q.data || {}

  if (d.error) {
    return (
      <>
        {lede}
        <div id="genhost">
          <div className="err">{d.error}</div>
        </div>
      </>
    )
  }

  const fw = d.framework || ''
  const items = d.generatables || []
  const hasModule = !!d.hasModule

  // "keel has no generators for <framework> yet" — the empty-catalogue state.
  if (!items.length) {
    return (
      <>
        {lede}
        <div id="genhost">
          <div className="card">
            <p className="muted" style={{ margin: 0 }}>
              keel has no generators for <b>{fw || 'this framework'}</b> yet.
            </p>
          </div>
        </div>
      </>
    )
  }

  return (
    <>
      {lede}
      <div id="genhost">
        <GenPlan dir={dir} fw={fw} items={items} hasModule={hasModule} />
      </div>
    </>
  )
}

// GenPlan owns the whole staged surface + its state (the original GEN.plan /
// GEN.openType / GEN.draft singleton). renderGenAll = the module header + one
// accordion per component type + the Generate/stack footer.
function GenPlan({
  dir,
  fw,
  items,
  hasModule,
}: {
  dir: string
  fw: string
  items: Generatable[]
  hasModule: boolean
}) {
  const con = useConsole()
  const genByKey = useMemo(() => {
    const m: Record<string, Generatable> = {}
    items.forEach((g) => (m[g.key] = g))
    return (key: string): Generatable | null => m[key] || null
  }, [items])
  const genModuleDef = useMemo(() => items.find((g) => g.level === 'module') || null, [items])

  // Default the target from the module def's target input, if it declares one
  // (renderGenerate's trailing seed). default is currently absent server-side, so
  // this resolves to '' — reproduced faithfully.
  const initialTarget = useMemo(() => {
    const tin = genModuleDef && genInputsOf(genModuleDef).find((i) => i.name === 'target')
    return (tin && tin.default) || ''
  }, [genModuleDef])

  const [plan, setPlan] = useState<Plan>({ vendor: '', module: '', target: initialTarget, components: [] })
  // openType — which section's Add form is open ('stack:'+key for a stack).
  const [openType, setOpenType] = useState<string | null>(null)
  // draft — the instance being added/edited, or null when nothing is open.
  const [draft, setDraft] = useState<Draft | null>(null)

  // --- section grouping (genSectionsHTML): accordions grouped by level, module
  // excluded (the module IS the header). A running counter numbers the badges.
  const byLevel: Record<string, Generatable[]> = {}
  items.forEach((g) => {
    ;(byLevel[g.level] = byLevel[g.level] || []).push(g)
  })

  // toggleGenType — open/close a section; opening a component section starts a
  // fresh draft, opening a stack (or closing) clears the draft. Ported verbatim.
  const toggleGenType = (key: string) => {
    const nextOpen = openType === key ? null : key
    setOpenType(nextOpen)
    if (nextOpen === key && !key.startsWith('stack:')) setDraft(newGenDraft(genByKey(key), key))
    else setDraft(null)
  }

  // --- draft param + field mutators -----------------------------------------
  const setDraftParam = (name: string, val: unknown) =>
    setDraft((prev) => (prev ? { ...prev, params: { ...prev.params, [name]: val } } : prev))
  const setField = (i: number, patch: Partial<Field>) =>
    setDraft((prev) =>
      prev ? { ...prev, fields: prev.fields.map((f, j) => (j === i ? { ...f, ...patch } : f)) } : prev,
    )
  const addGenField = () =>
    setDraft((prev) =>
      prev
        ? {
            ...prev,
            fields: [
              ...prev.fields,
              { name: '', type: 'string', nullable: false, unique: false, index: false, default: '', length: 0 },
            ],
          }
        : prev,
    )
  const delGenField = (i: number) =>
    setDraft((prev) => (prev ? { ...prev, fields: prev.fields.filter((_, j) => j !== i) } : prev))

  // --- staging actions (commitDraft/cancelDraft/editStaged/dupStaged/removeStaged)
  const commitDraft = (key: string) => {
    const g = genByKey(key)
    if (!draft) return
    // Enforce required inputs the same way the CLI would.
    for (const inp of genInputsOf(g)) {
      if (inp.required && inp.type !== 'fields' && !String(draft.params[inp.name] ?? '').trim()) {
        alert('“' + (inp.label || inp.name) + '” is required.')
        return
      }
    }
    const fields = draft.fields.filter((f) => String(f.name || '').trim())
    const inst: Component = { type: key, params: { ...draft.params }, fields }
    setPlan((prev) => {
      const comps = prev.components.slice()
      if (draft.editIdx >= 0) comps[draft.editIdx] = inst
      else comps.push(inst)
      return { ...prev, components: comps }
    })
    setDraft(newGenDraft(g, key))
  }
  const cancelDraft = (key: string) => setDraft(newGenDraft(genByKey(key), key))
  const editStaged = (i: number) => {
    const c = plan.components[i]
    if (!c) return
    setOpenType(c.type)
    setDraft({
      type: c.type,
      params: { ...c.params },
      fields: (c.fields || []).map((f) => ({ ...f })),
      editIdx: i,
    })
  }
  const dupStaged = (i: number) => {
    const c = plan.components[i]
    if (!c) return
    setPlan((prev) => {
      const comps = prev.components.slice()
      comps.splice(i + 1, 0, { type: c.type, params: { ...c.params }, fields: (c.fields || []).map((f) => ({ ...f })) })
      return { ...prev, components: comps }
    })
  }
  const removeStaged = (i: number) => {
    setPlan((prev) => ({ ...prev, components: prev.components.filter((_, j) => j !== i) }))
    if (draft && draft.editIdx === i) setDraft(null)
  }

  // --- submit / stack apply. Each streams through the shared console so the
  // generated output renders live below. ---

  // submitGenPlan — POST /api/generate {dir,vendor,module,target,components,dryRun}.
  const submitGenPlan = (dryRun: boolean) => {
    if (hasModule && !plan.module.trim()) {
      alert('Name the module first.')
      return
    }
    if (!plan.components.length) {
      alert('Stage at least one component.')
      return
    }
    con.stream('/api/generate', {
      dir,
      vendor: plan.vendor,
      module: plan.module,
      target: plan.target,
      components: plan.components,
      dryRun: !!dryRun,
    })
  }
  // addStack — install a stack's recipe(s) via keel add. --yes skips the TTY
  // confirm the exec path cannot answer; --trust covers a pack recipe.
  const addStack = (ids: string[]) => {
    con.stream('/api/exec', { dir, args: ['add', ...ids, '--yes', '--trust'] })
  }
  // "What will this do?" — a dry-run add, streamed to the console.
  const stackDryRun = (ids: string[]) => {
    con.stream('/api/exec', { dir, args: ['add', ...ids, '--dry-run'] })
  }

  // --- render (genHeaderHTML + genSectionsHTML + genFooterHTML) --------------
  let n = 0
  const sections: React.ReactNode[] = []
  LEVELS.forEach(([lvl, title]) => {
    const levelItems = (byLevel[lvl] || []).filter(() => lvl !== 'module') // the module IS the header
    if (!levelItems.length) return
    sections.push(
      <div className="msub" key={'sub-' + lvl}>
        {title}
      </div>,
    )
    levelItems.forEach((g) => {
      n++
      if (g.level === 'stack') {
        sections.push(
          <StackAccordion
            key={g.key}
            n={n}
            g={g}
            fw={fw}
            open={openType === 'stack:' + g.key}
            onToggle={() => toggleGenType('stack:' + g.key)}
            onAdd={addStack}
            onDryRun={stackDryRun}
          />,
        )
        return
      }
      sections.push(
        <ComponentAccordion
          key={g.key}
          n={n}
          g={g}
          plan={plan}
          open={openType === g.key}
          draft={draft}
          genByKey={genByKey}
          onToggle={() => toggleGenType(g.key)}
          onCommit={commitDraft}
          onCancel={cancelDraft}
          onEdit={editStaged}
          onDup={dupStaged}
          onRemove={removeStaged}
          onSetParam={setDraftParam}
          onSetField={setField}
          onAddField={addGenField}
          onDelField={delGenField}
        />,
      )
    })
  })

  return (
    <>
      <ModuleHeader plan={plan} fw={fw} hasModule={hasModule} moduleDef={genModuleDef} setPlan={setPlan} />
      {sections}
      <Footer plan={plan} fw={fw} hasModule={hasModule} onSubmit={submitGenPlan} />
    </>
  )
}

// --- module header (genHeaderHTML) -------------------------------------------
// Shown only for a framework with a real module concept. A framework with a
// vendor concept declares a "vendor" input; one with an app/code-vs-package
// target declares a "target" options input. Everything is read from GenInput.
function ModuleHeader({
  plan,
  fw,
  hasModule,
  moduleDef,
  setPlan,
}: {
  plan: Plan
  fw: string
  hasModule: boolean
  moduleDef: Generatable | null
  setPlan: React.Dispatch<React.SetStateAction<Plan>>
}) {
  if (!hasModule) return null
  const ins = genInputsOf(moduleDef)
  const hasVendor = ins.some((i) => i.name === 'vendor')
  const tin = ins.find((i) => i.name === 'target')
  const targetChoices = (tin && tin.choices) || []

  const pathNote = genWholePlan(fw) ? null : (
    <p className="muted" style={{ fontSize: 12, margin: 'var(--s3) 0 0' }}>
      {fw} has no whole-plan renderer yet, so Generate runs <code>keel gen</code> for each component. It looks and works
      the same; only the render path differs.
    </p>
  )

  return (
    <div className="card">
      <h3 style={{ marginTop: 0 }}>Module</h3>
      <div className="row" style={{ gap: 'var(--s2)', flexWrap: 'wrap' }}>
        {hasVendor && (
          <label style={{ flex: 1, minWidth: 160 }}>
            <span className="msub" style={{ paddingTop: 0 }}>
              Vendor
            </span>
            <input
              value={plan.vendor}
              placeholder="Acme"
              onInput={(e) => setPlan((prev) => ({ ...prev, vendor: (e.target as HTMLInputElement).value }))}
            />
          </label>
        )}
        <label style={{ flex: 1, minWidth: 160 }}>
          <span className="msub" style={{ paddingTop: 0 }}>
            Module
          </span>
          <input
            value={plan.module}
            placeholder={hasVendor ? 'Blog' : 'my_module'}
            onInput={(e) => setPlan((prev) => ({ ...prev, module: (e.target as HTMLInputElement).value }))}
          />
        </label>
      </div>
      {tin && targetChoices.length > 0 && (
        <div style={{ marginTop: 'var(--s3)' }}>
          <div className="msub" style={{ paddingTop: 0 }}>
            Target
          </div>
          <RList
            items={targetChoices.map((ch) => ({
              on: (plan.target || tin.default) === ch,
              label: targetLabel(ch),
              sub: targetHelp(ch),
              onClick: () => setPlan((prev) => ({ ...prev, target: ch })),
            }))}
          />
        </div>
      )}
      {pathNote}
    </div>
  )
}

// --- one accordion per component type (genComponentAccordion + genComponentBody)
function ComponentAccordion({
  n,
  g,
  plan,
  open,
  draft,
  genByKey,
  onToggle,
  onCommit,
  onCancel,
  onEdit,
  onDup,
  onRemove,
  onSetParam,
  onSetField,
  onAddField,
  onDelField,
}: {
  n: number
  g: Generatable
  plan: Plan
  open: boolean
  draft: Draft | null
  genByKey: (key: string) => Generatable | null
  onToggle: () => void
  onCommit: (key: string) => void
  onCancel: (key: string) => void
  onEdit: (i: number) => void
  onDup: (i: number) => void
  onRemove: (i: number) => void
  onSetParam: (name: string, val: unknown) => void
  onSetField: (i: number, patch: Partial<Field>) => void
  onAddField: () => void
  onDelField: (i: number) => void
}) {
  const staged = plan.components.map((c, i) => ({ c, i })).filter((x) => x.c.type === g.key)
  const summary = staged.length ? staged.length + ' staged' : 'none staged'
  const state = open ? 'open' : staged.length ? 'done' : ''

  const body = open ? (
    <>
      {staged.length ? (
        <div className="marows">
          {staged.map(({ c, i }) => (
            <StagedRow key={i} c={c} i={i} onEdit={onEdit} onDup={onDup} onRemove={onRemove} />
          ))}
        </div>
      ) : (
        <div className="ma-empty">No {g.label.toLowerCase()} staged yet. Fill the form below and Add.</div>
      )}
      <DraftForm
        g={g}
        draft={draft}
        genByKey={genByKey}
        onCommit={onCommit}
        onCancel={onCancel}
        onSetParam={onSetParam}
        onSetField={onSetField}
        onAddField={onAddField}
        onDelField={onDelField}
      />
    </>
  ) : null

  return <Accordion n={n} title={g.label} summary={summary} state={state} onToggle={onToggle} body={body} />
}

// genStagedRow — one staged instance: its name + field count, with edit /
// duplicate / remove.
function StagedRow({
  c,
  i,
  onEdit,
  onDup,
  onRemove,
}: {
  c: Component
  i: number
  onEdit: (i: number) => void
  onDup: (i: number) => void
  onRemove: (i: number) => void
}) {
  const nm = (c.params && (c.params.name as string)) || '(unnamed)'
  const fieldCount = c.fields && c.fields.length
  return (
    <div className="marow">
      <span className="ma-name">
        <b>{nm}</b>
        {fieldCount ? (
          <>
            {' '}
            <small>
              {fieldCount} field{fieldCount === 1 ? '' : 's'}
            </small>
          </>
        ) : null}
      </span>
      <span className="ma-acts">
        <button className="btn ghost sm" onClick={() => onEdit(i)} title="edit">
          ✎
        </button>
        <button className="btn ghost sm" onClick={() => onDup(i)} title="duplicate">
          ⧉
        </button>
        <button className="btn ghost sm" onClick={() => onRemove(i)} title="remove">
          ✕
        </button>
      </span>
    </div>
  )
}

// genDraftForm — the per-component typed form, rendered generically from the
// component's GenInput list, plus its Add/Save (+ Cancel while editing) button.
function DraftForm({
  g,
  draft,
  genByKey: _genByKey,
  onCommit,
  onCancel,
  onSetParam,
  onSetField,
  onAddField,
  onDelField,
}: {
  g: Generatable
  draft: Draft | null
  genByKey: (key: string) => Generatable | null
  onCommit: (key: string) => void
  onCancel: (key: string) => void
  onSetParam: (name: string, val: unknown) => void
  onSetField: (i: number, patch: Partial<Field>) => void
  onAddField: () => void
  onDelField: (i: number) => void
}) {
  // The draft belongs to this section only when it targets this component key
  // (the original re-seeded a mismatched GEN.draft; here toggleGenType guarantees
  // the open section owns the draft, so a mismatch just renders no form fields).
  const dr = draft && draft.type === g.key ? draft : null
  const editing = !!dr && dr.editIdx >= 0

  return (
    <div
      style={{
        marginTop: 'var(--s3)',
        borderTop: '1px dashed var(--line2)',
        paddingTop: 'var(--s3)',
      }}
    >
      {genInputsOf(g).map((inp) =>
        inp.type === 'fields' ? (
          <FieldsTable key={inp.name} fields={dr?.fields || []} onSetField={onSetField} onAddField={onAddField} onDelField={onDelField} />
        ) : (
          <ScalarControl key={inp.name} inp={inp} draft={dr} onSetParam={onSetParam} />
        ),
      )}
      <div className="row" style={{ gap: 'var(--s2)', marginTop: 'var(--s3)' }}>
        <button className="btn primary sm" onClick={() => onCommit(g.key)}>
          {editing ? 'Save changes' : '+ Add ' + g.label}
        </button>
        {editing && (
          <button className="btn ghost sm" onClick={() => onCancel(g.key)}>
            Cancel
          </button>
        )}
      </div>
    </div>
  )
}

// genScalarControl — one non-fields input from its GenInput: bool → checkbox,
// int → number, options → radio list, text/class/ref/path → a text box.
function ScalarControl({
  inp,
  draft,
  onSetParam,
}: {
  inp: GenInput
  draft: Draft | null
  onSetParam: (name: string, val: unknown) => void
}) {
  const raw = draft && draft.params[inp.name] !== undefined ? draft.params[inp.name] : ''
  const v = raw as string | number | boolean
  const label = (
    <span className="msub" style={{ paddingTop: 0 }}>
      {inp.label || inp.name}
      {inp.required ? ' *' : ''}
    </span>
  )
  const help = inp.help ? (
    <div className="muted" style={{ fontSize: 11, marginTop: 3 }}>
      {inp.help}
    </div>
  ) : null

  if (inp.type === 'bool') {
    return (
      <>
        <label className="chk" style={{ marginTop: 'var(--s3)' }}>
          <input type="checkbox" checked={!!v} onChange={(e) => onSetParam(inp.name, e.target.checked)} /> {inp.label || inp.name}
        </label>
        {help}
      </>
    )
  }
  if (inp.type === 'int') {
    return (
      <>
        <label style={{ display: 'block', marginTop: 'var(--s3)' }}>
          {label}
          <input
            type="number"
            value={v === '' || v === undefined ? '' : String(v)}
            onInput={(e) => onSetParam(inp.name, (e.target as HTMLInputElement).value)}
          />
        </label>
        {help}
      </>
    )
  }
  if (inp.type === 'options') {
    return (
      <>
        <div style={{ marginTop: 'var(--s3)' }}>
          {label}
          <RList
            items={(inp.choices || []).map((ch) => ({
              on: String(v) === String(ch),
              label: ch,
              onClick: () => onSetParam(inp.name, ch),
            }))}
          />
        </div>
        {help}
      </>
    )
  }
  // text / class / ref / path → a text box
  const ph = inp.name === 'name' ? 'e.g. Product' : ''
  return (
    <>
      <label style={{ display: 'block', marginTop: 'var(--s3)' }}>
        {label}
        <input
          value={v === '' || v === undefined ? '' : String(v)}
          placeholder={ph}
          onInput={(e) => onSetParam(inp.name, (e.target as HTMLInputElement).value)}
        />
      </label>
      {help}
    </>
  )
}

// genFieldsTable — the shared full fields table
// (name/type/nullable/unique/index/default/len).
function FieldsTable({
  fields,
  onSetField,
  onAddField,
  onDelField,
}: {
  fields: Field[]
  onSetField: (i: number, patch: Partial<Field>) => void
  onAddField: () => void
  onDelField: (i: number) => void
}) {
  return (
    <div style={{ marginTop: 'var(--s4)' }}>
      <div className="msub" style={{ paddingTop: 0 }}>
        Fields
      </div>
      <table className="ftable">
        <thead>
          <tr>
            <th>Name</th>
            <th style={{ width: 120 }}>Type</th>
            <th style={{ width: 52 }}>Null</th>
            <th style={{ width: 60 }}>Unique</th>
            <th style={{ width: 52 }}>Index</th>
            <th style={{ width: 90 }}>Default</th>
            <th style={{ width: 70 }}>Len</th>
            <th style={{ width: 36 }}></th>
          </tr>
        </thead>
        <tbody>
          {fields.length ? (
            fields.map((fld, i) => (
              <tr key={i}>
                <td>
                  <input
                    value={fld.name || ''}
                    placeholder="name"
                    onInput={(e) => onSetField(i, { name: (e.target as HTMLInputElement).value })}
                  />
                </td>
                <td>
                  <select value={fld.type} onChange={(e) => onSetField(i, { type: e.target.value })}>
                    {GEN_TYPES.map((t) => (
                      <option key={t} value={t}>
                        {t}
                      </option>
                    ))}
                  </select>
                </td>
                <td style={{ textAlign: 'center' }}>
                  <input
                    type="checkbox"
                    checked={fld.nullable}
                    onChange={(e) => onSetField(i, { nullable: e.target.checked })}
                  />
                </td>
                <td style={{ textAlign: 'center' }}>
                  <input
                    type="checkbox"
                    checked={fld.unique}
                    onChange={(e) => onSetField(i, { unique: e.target.checked })}
                  />
                </td>
                <td style={{ textAlign: 'center' }}>
                  <input
                    type="checkbox"
                    checked={fld.index}
                    onChange={(e) => onSetField(i, { index: e.target.checked })}
                  />
                </td>
                <td>
                  <input
                    value={fld.default || ''}
                    placeholder="—"
                    onInput={(e) => onSetField(i, { default: (e.target as HTMLInputElement).value })}
                  />
                </td>
                <td>
                  <input
                    type="number"
                    value={fld.length || ''}
                    placeholder="—"
                    onInput={(e) =>
                      onSetField(i, { length: parseInt((e.target as HTMLInputElement).value || '0', 10) })
                    }
                  />
                </td>
                <td style={{ textAlign: 'center' }}>
                  <button className="btn ghost sm" onClick={() => onDelField(i)} title="remove">
                    ✕
                  </button>
                </td>
              </tr>
            ))
          ) : (
            <tr>
              <td colSpan={8} className="muted" style={{ fontSize: 12 }}>
                No fields yet. Add one to give the model real columns.
              </td>
            </tr>
          )}
        </tbody>
      </table>
      <div className="row" style={{ marginTop: 'var(--s2)' }}>
        <button className="btn sm" onClick={onAddField}>
          + Add field
        </button>
      </div>
    </div>
  )
}

// --- stack section (genStackAccordion): auth etc., applied via keel add --------
function StackAccordion({
  n,
  g,
  fw,
  open,
  onToggle,
  onAdd,
  onDryRun,
}: {
  n: number
  g: Generatable
  fw: string
  open: boolean
  onToggle: () => void
  onAdd: (ids: string[]) => void
  onDryRun: (ids: string[]) => void
}) {
  const applies = g.applies || []
  const body = applies.length ? (
    <>
      <p className="muted" style={{ fontSize: 'var(--t-sm)', margin: '0 0 var(--s3)' }}>
        Installs: <code>{applies.join(' ')}</code>. A stack is a capability, not a code component. It applies the real
        recipe(s) via <code>keel add</code>. Review the plan in the stream first.
      </p>
      <div className="row" style={{ gap: 'var(--s2)' }}>
        <button className="btn primary sm" onClick={() => onAdd(applies)}>
          Add {g.label}
        </button>
        <button className="btn sm" onClick={() => onDryRun(applies)}>
          What will this do?
        </button>
      </div>
    </>
  ) : (
    <p className="muted" style={{ margin: 0 }}>
      This stack has no recipes to install for {fw}.
    </p>
  )
  return (
    <Accordion
      n={n}
      title={g.label}
      summary={applies.length ? applies.join(' ') : ''}
      state={open ? 'open' : ''}
      onToggle={onToggle}
      body={open ? body : null}
    />
  )
}

// --- footer (genFooterHTML): Generate the whole plan --------------------------
function Footer({
  plan,
  fw,
  hasModule,
  onSubmit,
}: {
  plan: Plan
  fw: string
  hasModule: boolean
  onSubmit: (dryRun: boolean) => void
}) {
  const n = plan.components.length
  const needModule = hasModule && !plan.module.trim()
  const dis = needModule || !n
  const note = hasModule
    ? plan.module.trim()
      ? 'module ' + plan.module
      : 'name the module above'
    : n
      ? 'ready to generate'
      : 'stage a component below'
  return (
    <div className="card" style={{ display: 'flex', alignItems: 'center', gap: 'var(--s3)', flexWrap: 'wrap' }}>
      <div style={{ flex: 1, minWidth: 200 }}>
        <b>
          {n} component{n === 1 ? '' : 's'} staged
        </b>
        <div className="muted" style={{ fontSize: 12 }}>
          {note} · {genWholePlan(fw) ? 'rendered as one plan' : 'one keel gen per component'}
        </div>
      </div>
      <button className="btn" disabled={dis} onClick={() => onSubmit(true)}>
        Preview
      </button>
      <button className="btn primary" disabled={dis} onClick={() => onSubmit(false)}>
        Generate
      </button>
    </div>
  )
}

// --- shared primitives (accHTML / rlistHTML), ported as components -------------

// Accordion is accHTML: the numbered/checked badge, a title + summary, a chevron,
// and the grid-based collapse. Only the open/done/'' states are used here (never
// locked), so the keyboard-toggle + aria wiring matches the original's non-locked
// path.
function Accordion({
  n,
  title,
  summary,
  state,
  onToggle,
  body,
}: {
  n: number
  title: string
  summary: string
  state: string
  onToggle: () => void
  body: React.ReactNode
}) {
  const open = state === 'open'
  const done = state === 'done'
  const cls = 'acc' + (open ? ' open' : '') + (done ? ' done' : '')
  const badge = done ? '✓' : String(n)
  return (
    <div className={cls}>
      <div
        className="acc-h"
        tabIndex={0}
        role="button"
        aria-expanded={open}
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            onToggle()
          }
        }}
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
          <div className="acc-body">{body}</div>
        </div>
      </div>
    </div>
  )
}

// RList is rlistHTML: a single-select radio list. Generate never passes a slug, so
// the icon slot is omitted (matching the original's `it.slug?…:""`).
function RList({
  items,
}: {
  items: { on: boolean; label: string; sub?: string; onClick: () => void }[]
}) {
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
          <span className="rl">
            <b>{it.label}</b>
            {it.sub ? <small>{it.sub}</small> : null}
          </span>
        </div>
      ))}
    </div>
  )
}
