import Button from './Button.jsx'
import { planList, perMonthLabel, termLabel, priceTag } from '../lib/plans.js'

// PlanCard (spec 02 §27). Everything shown comes from the plan object of /api/v1/plans.
//   <PlanCard plan featured saving={20} current selected cta="Выбрать" to|onClick primary
//             note={{tone:'warn', text:'Скоро закончится — продлите заранее'}} disabled disabledNote />
export default function PlanCard({ plan, featured = false, saving = 0, current = false, selected = false, cta = 'Выбрать', to, onClick, primary, loading, disabled, disabledNote, notes = [], free = false, priceLabel, className = '', style, children }) {
  const ribbon = current
    ? `Ваш тариф${saving > 0 ? ` · выгоднее на ${saving}%` : ''}`
    : featured && saving > 0 ? `Выгоднее на ${saving}%` : ''
  const cls = ['plan', featured || current ? 'plan--featured' : '', selected ? 'is-selected' : '', className].filter(Boolean).join(' ')
  const per = priceLabel ? '' : perMonthLabel(plan) // priceLabel: the price in Telegram Stars (Mini App)
  return (
    <div className={cls} style={style}>
      {ribbon && <span className="plan__ribbon">{ribbon}</span>}
      <h3 className="plan__name">{plan.name}</h3>
      <span className="plan__term">{termLabel(plan)}</span>
      <span className="plan__price">{free ? 'Бесплатно' : priceLabel || priceTag(plan.price_rub)}</span>
      {per && <span className="plan__per">{per}</span>}
      {notes.map((n) => <span key={n.text} className={`t-xs ${n.tone || 'subtle'}`}>{n.text}</span>)}
      <ul className="plan__list">
        {planList(plan).map((f) => <li key={f}>{f}</li>)}
      </ul>
      {children}
      {cta && (
        <Button
          variant={primary ?? (featured || current) ? 'primary' : 'secondary'}
          block
          to={to}
          onClick={onClick}
          loading={loading}
          disabled={disabled}
        >
          {cta}
        </Button>
      )}
      {disabled && disabledNote && <span className="t-xs subtle" style={{ marginTop: 6 }}>{disabledNote}</span>}
    </div>
  )
}
