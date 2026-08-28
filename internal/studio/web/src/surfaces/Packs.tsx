import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { z } from 'zod'
import { apiJSON } from '../lib/api'
import { Icon } from '../lib/icons'

// A recipe as a pack contributes it — the studio's recipeDTO subset the panel
// reads (id/label/kind drive the chip + its brand icon). Kept lax (passthrough)
// so unknown fields ride along untouched, mirroring the other studio schemas.
const packRecipe = z
  .object({
    id: z.string().optional().default(''),
    kind: z.string().optional().default(''),
    label: z.string().optional().default(''),
    lang: z.string().optional().default(''),
  })
  .passthrough()

// One recipe pack as /api/packs lists it (studio.go packDTO): identity, trust,
// enabled/builtin flags, and the recipes it ships into the Build catalog.
const packSchema = z
  .object({
    name: z.string(),
    version: z.string().optional().default(''),
    description: z.string().optional().default(''),
    trusted: z.boolean().optional().default(false),
    builtin: z.boolean().optional().default(false),
    // enabled defaults true: the original treats `enabled !== false` as on.
    enabled: z.boolean().optional().default(true),
    recipes: z.array(packRecipe).optional().default([]),
  })
  .passthrough()

const packsResponse = z
  .object({ packs: z.array(packSchema).optional().default([]) })
  .passthrough()

type Pack = z.infer<typeof packSchema>
type PackRecipe = z.infer<typeof packRecipe>

// loadPacks (original: the GET at the top of renderPacks). The original swallows
// a GET failure into an empty list, so the panel degrades to the empty state
// rather than an error — matched here by catching and returning no packs.
function usePacks() {
  return useQuery({
    queryKey: ['packs'],
    queryFn: async () => {
      try {
        const d = await apiJSON('/api/packs')
        return packsResponse.parse(d).packs
      } catch {
        return [] as Pack[]
      }
    },
  })
}

// togglePack / removePack (original): mutate the pack, then — because a pack's
// recipes join or leave the Build catalog — refresh the recipe list the Build
// tree reads. Invalidating ['recipes'] is the React twin of the original's
// re-fetch of /api/recipes into the R/LANGS globals; ['packs'] re-renders here.
// The handler answers a failed action with {ok:false,error}, which the original
// alert()s; a thrown error (network / non-2xx) is alert()ed as its string form.
function usePackAction() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (vars: { action: 'enable' | 'disable' | 'remove'; name: string }) => {
      const d = (await apiJSON('/api/packs', vars)) as { ok?: boolean; error?: string }
      if (d && d.ok === false) throw new Error(d.error || 'failed')
      return d
    },
    onError: (e) => {
      alert(String((e as Error).message || e))
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['recipes'] })
      await qc.invalidateQueries({ queryKey: ['packs'] })
    },
  })
}

// The recipe chip, rendered read-only (cursor:default) exactly as the original's
// `<span class="opt" style="cursor:default">iconImg(iconSlug(r)) label <small>kind</small></span>`.
// Icon renders the brand square, or nothing when the slug is unknown — matching
// iconImg(...) returning "".
function RecipeChip({ r }: { r: PackRecipe }) {
  return (
    <span className="opt" style={{ cursor: 'default' }}>
      <Icon r={{ id: r.id, label: r.label }} size={21} />
      <span>
        {r.label} <small>{r.kind}</small>
      </span>
    </span>
  )
}

function PackCard({ pack }: { pack: Pack }) {
  const action = usePackAction()
  const on = pack.enabled !== false
  const recipes = pack.recipes || []

  const trust = (
    <span className={'tag ' + (pack.trusted ? 'ok' : 'warn')}>{pack.trusted ? 'trusted' : 'untrusted'}</span>
  )

  return (
    <div className="card" style={on ? undefined : { opacity: 0.6 }}>
      <div className="row" style={{ width: '100%' }}>
        <span style={{ fontWeight: 600 }}>{pack.name}</span>
        <span className="grow" />
        <span className="tag">{pack.version || ''}</span>
        {/* Enabled shows just the trust tag; disabled shows a "disabled" tag first, then trust. */}
        {on ? (
          trust
        ) : (
          <>
            <span className="tag warn">disabled</span>
            {trust}
          </>
        )}
        {/* Built-in packs cannot be toggled or removed. */}
        {!pack.builtin &&
          (on ? (
            <button className="btn ghost" onClick={() => action.mutate({ action: 'disable', name: pack.name })}>
              Disable
            </button>
          ) : (
            <button className="btn primary" onClick={() => action.mutate({ action: 'enable', name: pack.name })}>
              Enable
            </button>
          ))}
        {!pack.builtin && (
          <button
            className="btn ghost"
            onClick={() => {
              if (
                !window.confirm(
                  'Remove pack "' +
                    pack.name +
                    '"?\n\nThis deletes its installed files and removes its recipes from the Build tree. Already-built projects are untouched.',
                )
              )
                return
              action.mutate({ action: 'remove', name: pack.name })
            }}
          >
            Remove
          </button>
        )}
      </div>

      {pack.description && <p className="muted" style={{ margin: '8px 0 0', fontSize: 12 }}>{pack.description}</p>}

      {on ? (
        <div className="opts" style={{ marginTop: 12 }}>
          {recipes.length ? (
            recipes.map((r, i) => <RecipeChip key={r.id || i} r={r} />)
          ) : (
            <span className="muted">no recipes</span>
          )}
        </div>
      ) : (
        <p className="muted" style={{ margin: '10px 0 0', fontSize: 12 }}>
          Disabled — its recipes are out of the Build tree. Enable to restore them.
        </p>
      )}
    </div>
  )
}

export default function Packs() {
  const { data: packs, isLoading } = usePacks()

  // The original renders nothing until the async list resolves; while it loads
  // the studio shows this muted line, consistent with the reference surface.
  if (isLoading || !packs) return <div className="muted">Loading…</div>

  return (
    <>
      <h1 className="page">Recipe packs</h1>
      <p className="lede">
        Packs are shareable recipe bundles: their recipes appear in the Build tree automatically. Disable one to drop
        its recipes without uninstalling it. Find more with <code>keel recipes search</code>; install with{' '}
        <code>keel recipes add &lt;owner/repo&gt;</code>.
      </p>
      {packs.length ? (
        packs.map((p) => <PackCard key={p.name} pack={p} />)
      ) : (
        <div className="muted">
          No packs installed. Add one with <code>keel recipes add &lt;owner/repo&gt;</code>.
        </div>
      )}
    </>
  )
}
