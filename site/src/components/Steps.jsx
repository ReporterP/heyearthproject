import { Link } from 'react-router-dom'

// Steps (spec 02 §21): «Тариф → Оформление → Оплата». Done steps are links back; the active one is
// aria-current="step"; future ones are grey. On a phone only the active step keeps its label.
//   <Steps items={[{ label: 'Тариф', to: '/payment' }, { label: 'Оформление' }, { label: 'Оплата' }]} current={1} />
export default function Steps({ items, current, label = 'Шаги оформления' }) {
  return (
    <ol className="steps" aria-label={label} style={{ margin: 0, padding: 0 }}>
      {items.map((it, i) => {
        const done = i < current
        const active = i === current
        const cls = done ? 'is-done' : active ? 'is-active' : ''
        const body = (
          <>
            <span className="n" aria-hidden="true">{done ? '✓' : i + 1}</span>
            <span className={active ? '' : 'max-sm:sr-only'}>{it.label}</span>
          </>
        )
        return [
          i > 0 ? <li key={`s${i}`} className="sep max-sm:w-3!" aria-hidden="true" /> : null,
          <li key={i} className={cls} aria-current={active ? 'step' : undefined}>
            {done && (it.to || it.onClick)
              ? it.to
                ? <Link to={it.to} className="row gap-2" style={{ color: 'inherit', textDecoration: 'none' }}>{body}</Link>
                : <button type="button" onClick={it.onClick} className="row gap-2" style={{ color: 'inherit', background: 'none', border: 0, padding: 0, font: 'inherit', cursor: 'pointer' }}>{body}</button>
              : body}
          </li>,
        ]
      })}
    </ol>
  )
}
