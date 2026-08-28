// A consistent stand-in for surfaces still being ported, using the studio's own
// page furniture so the shell reads as finished while the port proceeds.
export default function Placeholder({ title, lede }: { title: string; lede: string }) {
  return (
    <div>
      <h1 className="page">{title}</h1>
      <p className="lede">{lede}</p>
    </div>
  )
}
