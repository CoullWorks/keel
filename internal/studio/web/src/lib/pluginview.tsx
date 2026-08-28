// The shared View→React renderer, the port of the studio's renderScreenSection
// (internal/studio/index.html). A plugin Screener screen and a plugin Pager page
// both return the SAME data shape — a View of Sections, not markup — so keel
// draws them and a plugin can never script into the studio. This one renderer
// serves both PluginPage and the per-project screen tab.
//
// The View DTO is plugin.View / plugin.Section / plugin.Item (plugin/plugin.go):
//   View    { sections: Section[] }
//   Section { title, kind ("stat" | "list" | "text"), items: Item[] }
//   Item    { label, value, href? }   // href makes a row a link

import { safeURL } from './url'

// Item is one row. Href (http/https only) makes its label a link.
export type Item = { label?: string; value?: string; href?: string }
// Section is one block. Kind is "stat" (a row of big-number opts) or, for
// anything else ("list"/"text"), a stack of label/value rows.
export type Section = { title?: string; kind?: string; items?: Item[] }

// SectionView draws one Section, porting renderScreenSection's two branches:
//   - "stat": a wrapping row of .opt pills, each a bold value over a small label.
//   - list / text (the default): a vertical stack of rows, the label (optionally
//     a link) on the left and the muted value on the right.
function SectionView({ s }: { s: Section }) {
  const items = s.items || []
  let inner: React.ReactNode
  if (s.kind === 'stat') {
    inner = (
      <div className="opts">
        {items.map((it, i) => (
          <div className="opt" key={i} style={{ cursor: 'default' }}>
            <span>
              <b>{it.value}</b>
              <small>{it.label}</small>
            </span>
          </div>
        ))}
      </div>
    )
  } else {
    inner = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {items.map((it, i) => (
          <div className="row" key={i} style={{ gap: 10 }}>
            <span style={{ flex: 1 }}>
              {it.href ? (
                <a href={safeURL(it.href)} target="_blank" rel="noreferrer noopener">
                  {it.label}
                </a>
              ) : (
                it.label
              )}
            </span>
            {it.value ? (
              <span className="muted" style={{ fontSize: 12.5 }}>
                {it.value}
              </span>
            ) : null}
          </div>
        ))}
      </div>
    )
  }
  return (
    <div className="card">
      {s.title ? <h3>{s.title}</h3> : null}
      {inner}
    </div>
  )
}

// ScreenView draws a whole View — its sections, in order. Used by both the plugin
// page surface and the per-project screen tab.
export function ScreenView({ sections }: { sections: Section[] }) {
  return (
    <>
      {sections.map((s, i) => (
        <SectionView key={i} s={s} />
      ))}
    </>
  )
}
