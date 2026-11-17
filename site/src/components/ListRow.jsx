import { Link } from 'react-router-dom'
import Icon from './Icon.jsx'
import Badge from './Badge.jsx'

// ListRow (spec 02 §24): icon · title + subtitle · amount (mono) · status badge.
// With `to` the whole row is a link to the payment; on a phone the badge goes under the amount.
//   <ListRow icon="topup" title="Пополнение" sub="14 ноября, 21:47 · карта" amount="+1 000 ₽" amountTone="ok" badge={{tone:'ok', text:'Оплачено'}} to="/payments/501" />
export default function ListRow({ icon = 'receipt', title, sub, amount, amountTone, badge, to }) {
  const inner = (
    <>
      <span className="rowi__ic"><Icon name={icon} /></span>
      <span style={{ minWidth: 0 }}>
        <span className="rowi__t" style={{ display: 'block' }}>{title}</span>
        {sub && <span className="rowi__s" style={{ display: 'block' }}>{sub}</span>}
      </span>
      <span className="max-sm:flex max-sm:flex-col max-sm:items-end max-sm:gap-1" style={{ gridColumn: 'span 1' }}>
        {amount && <span className={`rowi__amt ${amountTone || ''}`}>{amount}</span>}
        {badge && <Badge tone={badge.tone} className="sm:hidden">{badge.text}</Badge>}
      </span>
      {badge ? <Badge tone={badge.tone} className="max-sm:hidden">{badge.text}</Badge> : <span />}
    </>
  )
  if (to) return <Link to={to} className="rowi">{inner}</Link>
  return <div className="rowi">{inner}</div>
}

// Group label in a history list («Ноябрь», «Ранее»).
export function Group({ children }) {
  return <p className="grp">{children}</p>
}
