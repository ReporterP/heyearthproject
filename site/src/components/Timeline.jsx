// Timeline (spec 02 §22): payment history. done — green dot; now — yellow pulsing ring («ждём»);
// future — empty dot, muted label.
//   <Timeline items={[{ title:'Платёж создан', sub:'21:47', state:'done' }, { title:'Ждём…', state:'now' }, { title:'Подписка оформлена' }]} />
export default function Timeline({ items }) {
  return (
    <ol className="timeline">
      {items.map((it) => (
        <li key={it.title} className={`${it.state === 'done' ? 'is-done' : it.state === 'now' ? 'is-now' : ''} ${it.rise ? 'rise' : ''}`}>
          <b className={it.state ? '' : 'muted'} style={{ display: 'block', fontWeight: 700 }}>{it.title}</b>
          {it.sub && <span className="t-sm subtle">{it.sub}</span>}
        </li>
      ))}
    </ol>
  )
}
