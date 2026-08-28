import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiJSON, fetchJSON } from '../../lib/api'
import { type Project, type Member, type Backend } from '../../lib/types'
import { Icon } from '../../lib/icons'
import { useConsole } from '../../lib/console'
import { clickable } from '../../lib/a11y'

// Data.tsx is the React port of the studio's Data tab (renderData + its helpers:
// dataPreflight, renderSQL/runRawSQL, projTables/openTable, loadGrid/renderGrid,
// editCell/commitEdits/cancelEdits, sortBy/gotoPage/applyGridFilter/clearGridFilter
// and followFK). It is a database browser: a Grid sub-tab (click a table, page,
// sort, filter, follow foreign keys, stage inline edits, Commit) and a Raw SQL
// escape hatch.
//
// The original wrote every async result through gridSet(html), which was a NO-OP
// unless the grid host was still on screen AND the Data tab was still active —
// the race guard that stopped a stale post-await write from crashing on a null
// host or clobbering a newer view. Here the equivalent guards are: React Query
// keyed by (path, table, page, …) so a stale fetch is ignored by key, and the
// local sub-tab/table/page state that drives those keys, so switching away or
// moving to a new table simply keys off the old request.

// --- server shapes (see internal/studio/db.go) ---
type FK = { table: string; column: string }
type Column = { name: string; type: string; fk?: FK }
type GridResult = {
  table: string
  columns: Column[]
  rows: (string | null)[][]
  pk: string[]
  page: number
  pageSize: number
  total: number
}
type PingResult = { reachable?: boolean; reason?: string }
type TablesResult = { tables?: string[]; error?: string }

// A staged inline edit, keyed rowIdx|col, mirroring GRID.dirty.
type Dirty = { rowIdx: number; col: string; value: string | null; key: Record<string, string> }

const PAGE_SIZE = 50

// isNumType right-aligns numeric columns, ported verbatim.
const isNumType = (t?: string) => /int|numeric|decimal|real|double|float|serial|money/i.test(t || '')
const dirtyKey = (rowIdx: number, col: string) => rowIdx + '|' + col

export default function DataTab({ p, be }: { p: Project | Member; be: Backend | null }) {
  const con = useConsole()
  // Local sub-tab state (grid|sql) — the original TAB.pdata, defaulting to grid.
  const [sub, setSub] = useState<'grid' | 'sql'>('grid')

  const dir = p.path
  const banner = be && be.inherited && be.engine

  return (
    <>
      <p className="lede" style={{ marginTop: 0 }}>
        Browse and edit <b>{p.name}</b>'s database. The grid is a real driver. Click a table, page, sort, filter, follow
        foreign keys, and stage inline edits that Commit applies in one transaction. Raw SQL is the escape hatch for DDL
        and one-offs.
      </p>

      {banner && (
        <div className="backend" style={{ marginBottom: 14 }}>
          <Icon r={{ id: be?.provider || be?.engine, label: be?.engine }} size={22} />
          <div style={{ flex: 1 }}>
            <div className="bl">Inherited shared backend</div>
            <b>
              {be?.engine}
              {be?.provider ? ' · ' + be.provider : ''}
            </b>
            {be?.source && (
              <span className="muted" style={{ fontSize: 12 }}>
                {' '}
                · {be.source}
              </span>
            )}
          </div>
        </div>
      )}

      {/* dbBar — the real `keel db …` verbs, streamed on /api/exec. Reset is
          destructive, so it confirms first. */}
      <div className="row" style={{ gap: 6, flexWrap: 'wrap', margin: '2px 0 12px' }}>
        <span className="muted" style={{ fontSize: 12, alignSelf: 'center', marginRight: 2 }}>
          Database:
        </span>
        <button
          className="btn sm"
          onClick={() => con.stream('/api/exec', { dir, args: ['db', 'migrate'] })}
          title="Run outstanding migrations"
        >
          Migrate
        </button>
        <button
          className="btn sm"
          onClick={() => con.stream('/api/exec', { dir, args: ['db', 'seed'] })}
          title="Run the database seeders"
        >
          Seed
        </button>
        <button
          className="btn sm"
          onClick={() => con.stream('/api/exec', { dir, args: ['db', 'status'] })}
          title="Show migration status"
        >
          Status
        </button>
        <button
          className="btn sm warn"
          onClick={() => {
            if (confirm('Reset drops and rebuilds the database. Continue?'))
              con.stream('/api/exec', { dir, args: ['db', 'reset'] })
          }}
          title="Drop, re-migrate and re-seed (destructive)"
        >
          Reset
        </button>
      </div>

      {/* tabStrip("pdata") — Grid | Raw SQL */}
      <div className="tabs">
        <div className={'tab ' + (sub === 'grid' ? 'on' : '')} {...clickable(() => setSub('grid'))}>
          Grid
        </div>
        <div className={'tab ' + (sub === 'sql' ? 'on' : '')} {...clickable(() => setSub('sql'))}>
          Raw SQL
        </div>
      </div>

      <div id="gridhost" style={{ minHeight: 200 }}>
        {sub === 'sql' ? <SQLPane dir={dir} /> : <GridPane dir={dir} />}
      </div>
    </>
  )
}

// SQLPane is renderSQL + runRawSQL: a free-form SQL box that streams through the
// project's DB client via the shared console (con.stream('/api/db/query', …)).
function SQLPane({ dir }: { dir: string }) {
  const con = useConsole()
  const [sql, setSql] = useState('')
  const runRawSQL = () => {
    if (sql.trim()) con.stream('/api/db/query', { dir, sql })
  }
  return (
    <div className="card">
      <h3>Raw SQL</h3>
      <p className="muted" style={{ fontSize: 12.5, margin: '0 0 10px' }}>
        Runs free-form SQL through this project's DB client (ddev psql / docker compose exec). Streams to the console
        below. The grid is the everyday tool; this is for DDL, EXPLAIN and one-offs.
      </p>
      <textarea
        id="rawsql"
        placeholder="SELECT * FROM users LIMIT 10;"
        style={{ minHeight: 120 }}
        value={sql}
        onChange={(e) => setSql(e.target.value)}
      />
      <div className="row" style={{ marginTop: 10 }}>
        <button className="btn primary" onClick={runRawSQL}>
          Run SQL
        </button>
      </div>
    </div>
  )
}

// GridPane is the grid half: dataPreflight → projTables → loadGrid/renderGrid,
// with the same not-reachable / loading / empty / error / grid states.
function GridPane({ dir }: { dir: string }) {
  const con = useConsole()
  // The open table (null = the tables list). Selecting a table resets paging,
  // sort, filter and staged edits, exactly like openTable.
  const [table, setTable] = useState<string | null>(null)

  // Pre-flight the database before offering the grid. Never shows the raw driver
  // error — only a calm, actionable state. Keyed by dir so it refetches per
  // project; retry:false so a "not reachable" is a normal answer, not a retry
  // storm. refetchOnMount forces a fresh check each time the Data tab opens (the
  // original re-ran dataPreflight on every renderData).
  const ping = useQuery({
    queryKey: ['db-ping-data', dir],
    queryFn: async (): Promise<PingResult> => {
      try {
        return await apiJSON<PingResult>('/api/db/ping?dir=' + encodeURIComponent(dir))
      } catch {
        return { reachable: false, reason: 'Database not reachable - start the environment, then reopen Data.' }
      }
    },
    retry: false,
    refetchOnMount: 'always',
  })

  // "checking the database…" gate — while the ping is in flight.
  if (ping.isFetching && ping.data === undefined) {
    return <span className="muted">checking the database…</span>
  }

  if (ping.data && !ping.data.reachable) {
    const reason = ping.data.reason || 'Database not reachable - start the environment.'
    const configure = /configure/i.test(ping.data.reason || '')
    return (
      <div className="card">
        <h3 style={{ marginTop: 0 }}>Database not reachable</h3>
        <p className="muted" style={{ fontSize: 13, margin: '0 0 12px' }}>
          {reason}
        </p>
        <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
          {!configure && (
            <button className="btn sm" onClick={() => con.stream('/api/project/action', { dir, action: 'start' })}>
              ▶ Start environment
            </button>
          )}
          <button className="btn sm ghost" onClick={() => ping.refetch()}>
            ↻ Retry
          </button>
        </div>
      </div>
    )
  }

  // Reachable: a table is open → the grid; otherwise the tables list.
  if (table !== null) {
    return <TableGrid dir={dir} table={table} onBack={() => setTable(null)} onOpenTable={(t) => setTable(t)} />
  }
  return <TablesList dir={dir} onOpen={(t) => setTable(t)} />
}

// TablesList is projTables: the click-a-table chip list, with loading / error /
// empty states. Keyed by dir so a stale fetch from a previous project is ignored.
function TablesList({ dir, onOpen }: { dir: string; onOpen: (t: string) => void }) {
  const q = useQuery({
    queryKey: ['db-tables', dir],
    queryFn: () => fetchJSON<TablesResult>('/api/db/tables', { dir }),
    retry: false,
    refetchOnMount: 'always',
  })

  if (q.isFetching && q.data === undefined) return <span className="muted">loading tables…</span>

  const d = q.data
  if (!d) return <span className="muted">loading tables…</span>
  if ('error' in d && d.error) return <div className="err">{d.error}</div>

  const tables = (d as TablesResult).tables || []
  if (!tables.length) {
    return (
      <span className="muted">
        No tables found. Is the database running? Start the env in Projects, run Migrate, then reopen Data.
      </span>
    )
  }
  return (
    <>
      <div className="muted" style={{ marginBottom: 8 }}>
        {tables.length} tables · click one to open the grid
      </div>
      <div>
        {tables.map((tb) => (
          <button key={tb} className="btn ghost sm" style={{ margin: 3 }} onClick={() => onOpen(tb)}>
            {tb}
          </button>
        ))}
      </div>
    </>
  )
}

// TableGrid is loadGrid + renderGrid + the edit/commit machinery. It owns the
// paging/sort/filter/dirty state the original kept on the GRID singleton; the
// fetch is keyed on (dir, table, page, sort, desc, filterCol, filter) so a stale
// response for an older page/sort/filter is ignored by key — the guarded-update
// behaviour the original got from gridSet returning false.
function TableGrid({
  dir,
  table,
  onBack,
  onOpenTable,
}: {
  dir: string
  table: string
  onBack: () => void
  onOpenTable: (t: string) => void
}) {
  const [page, setPage] = useState(1)
  const [sort, setSort] = useState('')
  const [desc, setDesc] = useState(false)
  const [filterCol, setFilterCol] = useState('')
  const [filter, setFilter] = useState('')
  const [dirty, setDirty] = useState<Record<string, Dirty>>({})
  // filterInput holds the filter box's live text until Filter/Enter applies it,
  // mirroring the original DOM input that only updated GRID.filter on apply.
  const [filterInput, setFilterInput] = useState('')
  // The cell being edited inline (row/col indices), or null.
  const [editing, setEditing] = useState<{ ri: number; ci: number } | null>(null)

  const body = { dir, table, page, pageSize: PAGE_SIZE, sort, desc, filterCol, filter }
  const q = useQuery({
    queryKey: ['db-grid', dir, table, page, sort, desc, filterCol, filter],
    queryFn: () => fetchJSON<GridResult>('/api/db/grid', body),
    retry: false,
    refetchOnMount: 'always',
  })

  // Default the filter column to the first column once the grid loads, and keep
  // the filter box in sync with the applied filter (Clear resets both). Mirrors
  // renderGrid's trailing `if(!GRID.filterCol&&cols.length)…` and the input's
  // value={GRID.filter}.
  const d = q.data
  const grid: GridResult | null = d && !('error' in d && d.error) ? (d as GridResult) : null
  const cols = grid?.columns ?? []
  useEffect(() => {
    if (!filterCol && cols.length) setFilterCol(cols[0].name)
  }, [filterCol, cols])
  useEffect(() => {
    setFilterInput(filter)
  }, [filter])

  // followFK — load the referenced table filtered to the linked value, resetting
  // paging/sort/edits (the original mutated GRID then called loadGrid).
  const followFK = (refTable: string, refCol: string, value: string) => {
    setPage(1)
    setSort('')
    setDesc(false)
    setDirty({})
    setEditing(null)
    setFilterCol(refCol)
    setFilter(value)
    onOpenTable(refTable)
  }
  const sortBy = (col: string) => {
    if (sort === col) setDesc((v) => !v)
    else {
      setSort(col)
      setDesc(false)
    }
    setPage(1)
  }
  const gotoPage = (pp: number) => setPage(pp)
  const applyGridFilter = () => {
    setFilter(filterInput.trim())
    setPage(1)
  }
  const clearGridFilter = () => {
    setFilter('')
    setPage(1)
  }
  const cancelEdits = () => setDirty({})

  // commitEdits — POST /api/db/commit {dir, table, edits}; on success clear the
  // staged edits and reload the current page.
  const commitEdits = async () => {
    const edits = Object.values(dirty).map((dd) => ({ column: dd.col, value: dd.value, key: dd.key }))
    if (!edits.length) return
    const res = await fetchJSON<{ committed?: boolean; error?: string }>('/api/db/commit', { dir, table, edits })
    if ('error' in res && res.error) {
      alert('Commit failed: ' + res.error)
      return
    }
    setDirty({})
    setEditing(null)
    q.refetch()
  }

  // rowKey builds the primary-key map for a row, so an UPDATE targets one row.
  const rowKey = (row: (string | null)[]): Record<string, string> => {
    const k: Record<string, string> = {}
    cols.forEach((c, i) => {
      if (grid!.pk.includes(c.name)) k[c.name] = row[i] as string
    })
    return k
  }

  // stageEdit is editCell's commit: compares against the original cell and either
  // stages the change or drops it if it reverted to the original value.
  const stageEdit = (ri: number, ci: number, inputVal: string) => {
    const col = cols[ci].name
    const dk = dirtyKey(ri, col)
    const orig = grid!.rows[ri][ci]
    const newVal = inputVal === '' && orig === null ? null : inputVal
    setDirty((prev) => {
      const next = { ...prev }
      if (newVal === orig) delete next[dk]
      else next[dk] = { rowIdx: ri, col, value: newVal, key: rowKey(grid!.rows[ri]) }
      return next
    })
    setEditing(null)
  }

  const backBtn = (
    <button className="btn ghost sm" onClick={onBack}>
      ← Tables
    </button>
  )

  if (q.isFetching && q.data === undefined) {
    // loadGrid awaited before its first render; the original left the prior host
    // content in place. A brief loading line keeps the transition calm.
    return <span className="muted">loading…</span>
  }
  if (d && 'error' in d && d.error) {
    return (
      <>
        <div className="grid-toolbar">{backBtn}</div>
        <div className="err">{d.error}</div>
      </>
    )
  }
  if (!grid) return <span className="muted">loading…</span>

  const editable = grid.pk.length > 0
  const dirtyCount = Object.keys(dirty).length
  const pages = Math.max(1, Math.ceil(grid.total / grid.pageSize))

  return (
    <>
      <div className="grid-toolbar">
        {backBtn}
        <b>{grid.table}</b>
        <span className="grow" />
        <select
          style={{ width: 'auto', padding: '5px 8px', fontSize: 12.5 }}
          value={filterCol}
          onChange={(e) => setFilterCol(e.target.value)}
        >
          {cols.map((c) => (
            <option key={c.name} value={c.name}>
              {c.name}
            </option>
          ))}
        </select>
        <input
          id="gridfilter"
          placeholder="filter contains…"
          value={filterInput}
          onChange={(e) => setFilterInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') applyGridFilter()
          }}
        />
        <button className="btn sm" onClick={applyGridFilter}>
          Filter
        </button>
        {filter && (
          <button className="btn ghost sm" onClick={clearGridFilter}>
            Clear
          </button>
        )}
        {dirtyCount > 0 && (
          <>
            <button className="btn primary sm" onClick={commitEdits}>
              Commit {dirtyCount}
            </button>
            <button className="btn ghost sm" onClick={cancelEdits}>
              Cancel
            </button>
          </>
        )}
      </div>

      {!editable && (
        <div className="err" style={{ marginBottom: 8 }}>
          read-only: this table has no primary key, so cells can't be edited
        </div>
      )}

      <div className="grid-wrap">
        <table className="grid">
          <thead>
            <tr>
              {cols.map((c) => {
                const pk = grid.pk.includes(c.name)
                const arrow = sort === c.name ? (desc ? ' ↓' : ' ↑') : ''
                return (
                  <th
                    key={c.name}
                    className={pk ? 'pk' : ''}
                    {...clickable(() => sortBy(c.name))}
                    title="click to sort"
                  >
                    {c.name}
                    <span className="ty">{c.type}</span>
                    {arrow}
                  </th>
                )
              })}
            </tr>
          </thead>
          <tbody>
            {grid.rows.length ? (
              grid.rows.map((row, ri) => (
                <tr key={ri}>
                  {cols.map((c, ci) => {
                    const dk = dirtyKey(ri, c.name)
                    const staged = Object.prototype.hasOwnProperty.call(dirty, dk)
                    const val = staged ? dirty[dk].value : row[ci]
                    const isPk = grid.pk.includes(c.name)
                    const canEdit = editable && !isPk
                    const isEditing = editing !== null && editing.ri === ri && editing.ci === ci

                    const cls: string[] = []
                    if (isNumType(c.type)) cls.push('num')
                    if (val === null || val === undefined) cls.push('null')
                    if (staged) cls.push('dirty')
                    if (canEdit) cls.push('editable')

                    const disp = val === null || val === undefined ? 'NULL' : val

                    if (isEditing) {
                      const cur = staged ? dirty[dk].value : row[ci]
                      return (
                        <td key={ci} className={cls.join(' ')}>
                          <CellEditor
                            initial={cur === null || cur === undefined ? '' : String(cur)}
                            onCommit={(v) => stageEdit(ri, ci, v)}
                            onCancel={() => setEditing(null)}
                          />
                        </td>
                      )
                    }

                    if (c.fk && val !== null && val !== undefined) {
                      cls.push('fk')
                      return (
                        <td
                          key={ci}
                          className={cls.join(' ')}
                          onClick={canEdit ? () => setEditing({ ri, ci }) : undefined}
                        >
                          {disp}{' '}
                          <span
                            className="fklink"
                            role="button"
                            tabIndex={0}
                            title={`open ${c.fk.table}.${c.fk.column} = ${disp}`}
                            onClick={(e) => {
                              e.stopPropagation()
                              followFK(c.fk!.table, c.fk!.column, String(val))
                            }}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter' || e.key === ' ') {
                                e.preventDefault()
                                e.stopPropagation()
                                followFK(c.fk!.table, c.fk!.column, String(val))
                              }
                            }}
                          >
                            ↗
                          </span>
                        </td>
                      )
                    }
                    return (
                      <td
                        key={ci}
                        className={cls.join(' ')}
                        onClick={canEdit ? () => setEditing({ ri, ci }) : undefined}
                      >
                        {disp}
                      </td>
                    )
                  })}
                </tr>
              ))
            ) : (
              <tr>
                <td colSpan={cols.length || 1} className="muted" style={{ padding: 12 }}>
                  no rows
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="grid-pager">
        <button className="btn ghost sm" disabled={page <= 1} onClick={() => gotoPage(page - 1)}>
          ‹ Prev
        </button>
        <span>
          page {page} / {pages} · {grid.total} rows
        </span>
        <button className="btn ghost sm" disabled={page >= pages} onClick={() => gotoPage(page + 1)}>
          Next ›
        </button>
      </div>
    </>
  )
}

// CellEditor is editCell's inline input: focus + select on mount, commit on blur
// or Enter, revert (cancel) on Escape — the same handlers the original wired onto
// the created <input>.
function CellEditor({
  initial,
  onCommit,
  onCancel,
}: {
  initial: string
  onCommit: (v: string) => void
  onCancel: () => void
}) {
  const ref = useRef<HTMLInputElement>(null)
  const [v, setV] = useState(initial)
  // escaped guards the blur handler so an Escape-triggered blur reverts rather
  // than commits (the original called renderGrid() on Escape, discarding the DOM
  // input before its onblur could fire).
  const escaped = useRef(false)
  useEffect(() => {
    ref.current?.focus()
    ref.current?.select()
  }, [])
  return (
    <input
      ref={ref}
      value={v}
      onChange={(e) => setV(e.target.value)}
      onBlur={() => {
        if (escaped.current) return
        onCommit(v)
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault()
          onCommit(v)
        } else if (e.key === 'Escape') {
          escaped.current = true
          onCancel()
        }
      }}
    />
  )
}
