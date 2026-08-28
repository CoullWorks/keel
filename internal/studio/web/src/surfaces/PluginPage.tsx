import { useQuery } from '@tanstack/react-query'
import { apiJSON } from '../lib/api'
import { ScreenView, type Section } from '../lib/pluginview'
import { PluginComponent } from '../lib/pluginmount'

// PluginPage is the React port of the studio's renderPluginPage (the DATA-View
// path in internal/studio/index.html). A plugin Pager page contributes a global
// studio destination under the Extend nav. There are two kinds:
//   - a DATA page (!html): POST /api/plugin/page {id} returns a View the studio
//     draws (sections, no markup — a plugin cannot script into the studio).
//   - an own-UI page (html): the original hosts the plugin's own HTML in a
//     sandboxed iframe (renderPluginWebview). That iframe path is the REJECTED
//     approach and is deliberately NOT ported — an html page shows a placeholder
//     card until the component runtime lands.

// The plugin-pages meta the nav + this surface read, from /api/plugin-pages
// (handlePluginPages in internal/studio/plugins.go). Kept to the fields drawn.
export type PluginPageMeta = {
  id: string
  title: string
  icon?: string
  // html marks an own-UI page (iframe in the original) rather than a data View.
  html?: boolean
  // component is the /plugin-assets URL of a built React bundle the studio mounts.
  component?: string
  plugin: string
}

// usePluginPages fetches the global plugin pages once (['plugin-pages']); the
// nav in App.tsx shares this exact query key so the two stay in step. A failed
// fetch degrades to [] (the original loadPluginPages' catch), never an error.
export function usePluginPages() {
  return useQuery({
    queryKey: ['plugin-pages'],
    queryFn: () =>
      apiJSON<{ pages?: PluginPageMeta[] }>('/api/plugin-pages')
        .then((d) => d.pages || [])
        .catch(() => [] as PluginPageMeta[]),
  })
}

// pageHead is the port of the original pageHead(title, lede) — the page's title
// and lede, the same furniture every top-level surface uses.
function PageHead({ title, lede }: { title: string; lede: string }) {
  return (
    <>
      <h1 className="page">{title}</h1>
      {lede ? <p className="lede">{lede}</p> : null}
    </>
  )
}

// PageBody renders the actual page contents for a resolved data page: it fetches
// the View (POST /api/plugin/page {id}) and draws its sections. Loading, error,
// and empty states mirror the original renderPluginPage (loadInto + the
// "This page returned nothing." empty state).
function PageBody({ id }: { id: string }) {
  const q = useQuery({
    queryKey: ['plugin-page', id],
    queryFn: () => apiJSON<{ sections?: Section[]; error?: string }>('/api/plugin/page', { id }),
    retry: false,
  })

  if (q.isLoading || (!q.data && !q.isError)) {
    return (
      <div id="ppagehost">
        <span className="muted">loading…</span>
      </div>
    )
  }
  // A thrown fetch, or a handler that answered {error}, both surface inline as
  // the original loadInto does (the host's .err box).
  const err = q.isError ? String((q.error as Error)?.message || q.error) : q.data?.error
  if (err) {
    return (
      <div id="ppagehost">
        <div className="err">{err}</div>
      </div>
    )
  }
  const sections = q.data?.sections || []
  return (
    <div id="ppagehost">
      {sections.length ? (
        <ScreenView sections={sections} />
      ) : (
        <div className="muted">This page returned nothing.</div>
      )}
    </div>
  )
}

export default function PluginPage({ pageId }: { pageId: string }) {
  const { data: pages, isLoading, isError, error } = usePluginPages()

  if (isLoading || (!pages && !isError)) {
    return <div className="muted">Loading…</div>
  }
  if (isError) {
    return <div className="err">{String((error as Error)?.message || error)}</div>
  }

  const meta = (pages || []).find((p) => p.id === pageId)
  const title = meta?.title || pageId

  // An html page is the rejected iframe path: a small placeholder card until the
  // component runtime arrives, rather than porting renderPluginWebview.
  const body = !meta ? (
    <div id="ppagehost">
      <div className="err">no such page: {pageId}</div>
    </div>
  ) : meta.component ? (
    <PluginComponent url={meta.component} plugin={meta.plugin} />
  ) : meta.html ? (
    <div className="card">
      <h3 style={{ marginTop: 0 }}>Own-UI page</h3>
      <p className="muted" style={{ margin: 0, fontSize: 13 }}>
        This plugin ships its UI as HTML (the legacy iframe tier). Rebuild it as a
        component bundle to mount it here.
      </p>
    </div>
  ) : (
    <PageBody id={meta.id} />
  )

  return (
    <div>
      <PageHead title={title} lede="A page contributed by a plugin." />
      {body}
    </div>
  )
}
