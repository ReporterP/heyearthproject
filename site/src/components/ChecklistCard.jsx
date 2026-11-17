import { Link } from 'react-router-dom'

// «Как начать» (spec 08 §8.1 item 15, 02 §33 `chk`): three steps; a step is ticked by clicking its
// circle (kept in the browser) or, for «Нажмите «Добавить в Happ»», by pressing that button. The card
// disappears once all three are done.
const STEPS = ['Установите Happ', 'Нажмите «Добавить в Happ»', 'Включите подключение переключателем в Happ']

export default function ChecklistCard({ done, onToggle }) {
  const count = STEPS.filter((_, i) => done[i + 1]).length
  if (count === STEPS.length) return null
  const firstOpen = STEPS.findIndex((_, i) => !done[i + 1])
  return (
    <section className="card" aria-labelledby="chk-t">
      <div className="card__head"><h2 id="chk-t" className="card__title">Как начать</h2><span className="t-sm subtle">{count} из {STEPS.length}</span></div>
      <ol>
        {STEPS.map((t, i) => {
          const on = !!done[i + 1]
          return (
            <li key={t} className={`chk ${on ? 'is-done' : ''}`}>
              <button
                type="button"
                className="chk__d"
                aria-pressed={on}
                aria-label={`${on ? 'Снять отметку' : 'Отметить выполненным'}: ${t}`}
                onClick={() => onToggle(i + 1)}
                style={{ background: on ? undefined : 'transparent', cursor: 'pointer', padding: 0 }}
              >
                {on ? <span className="pop" style={{ display: 'grid' }}>✓</span> : i + 1}
              </button>
              <span className="chk__t">{i === firstOpen ? <b>{t}</b> : t}</span>
              {i === firstOpen ? <Link to="/help#connect" className="t-sm link">Не получается?</Link> : <span />}
            </li>
          )
        })}
      </ol>
    </section>
  )
}
