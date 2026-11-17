import { Link } from 'react-router-dom'
import Badge from './Badge.jsx'
import Button from './Button.jsx'
import Flap from './Flap.jsx'
import { Progress } from './Controls.jsx'
import { date, kop, money } from '../lib/format.js'
import { termShare } from '../lib/account.js'

// «Подписка» on the overview (spec 08 §8.1 item 9–11): plan, end date, days left as flap,
// term progress, what auto-renewal will do, «Продлить» / «Сменить тариф».
export function renewText(sub, ar) {
  if (ar?.free) return { lead: 'Подписка по промокоду', text: 'продлевается бесплатно.', short: 'Подписка по промокоду продлевается бесплатно' }
  const enabled = ar ? ar.enabled : sub?.auto_renew
  const at = date(ar?.renew_at || sub?.valid_until)
  const price = ar ? kop(ar.price_kop) : money(sub?.price_rub || 0)
  if (enabled) return { lead: 'Автопродление включено.', text: `${at} спишем ${price} с кошелька, если на балансе хватит.`, short: `Автопродление включено · ${price} с кошелька` }
  return { off: true, text: `Автопродление выключено — подписка закончится ${at}` }
}

export default function SubscriptionCard({ sub, plan, autoRenew, phone, started }) {
  const { left, pct } = termShare(sub, plan, started)
  const warn = left <= 3
  const renew = renewText(sub, autoRenew)
  const slug = sub?.plan || plan?.slug
  const flap = <Flap value={left} suffix="дн" size={phone ? 34 : 44} tone={warn ? 'warn' : 'accent'} label={`Осталось ${left} дн`} />
  return (
    <section className="card" style={{ display: 'flex', flexDirection: 'column' }}>
      <div className="card__head"><h2 className="card__title">Подписка</h2><Badge tone="ok">Активна</Badge></div>
      {phone ? (
        <div className="row between" style={{ alignItems: 'flex-end' }}>
          <span><b className="t-h3">{sub.plan_name}</b><br /><span className="t-sm subtle">до {date(sub.valid_until)}</span></span>
          {flap}
        </div>
      ) : (
        <>
          <p className="t-h2">{sub.plan_name}</p>
          <p className="muted t-sm">до {date(sub.valid_until, { year: true })}</p>
          <div className="row between" style={{ marginTop: 22, alignItems: 'flex-end' }}><span className="t-sm muted">Осталось</span>{flap}</div>
        </>
      )}
      {pct !== null && <div style={{ marginTop: 14 }}><Progress value={pct} warn={warn} label="Осталось от срока подписки" /></div>}
      {phone ? (
        <p className="t-xs subtle" style={{ marginTop: 10 }}>{renew.off ? <>{renew.text}. <Link to="/payments" className="link">Включить</Link></> : renew.short}</p>
      ) : (
        <p className="t-sm muted" style={{ marginTop: 16 }}>
          {renew.off
            ? <>{renew.text}. <Link to="/payments" className="link">Включить</Link></>
            : <><b style={{ color: 'var(--fg)' }}>{renew.lead}</b> {renew.text}</>}
        </p>
      )}
      {!phone && (
        <div className="row gap-2" style={{ marginTop: 'auto', paddingTop: 18 }}>
          <Button variant="secondary" to={slug ? `/payment?plan=${encodeURIComponent(slug)}` : '/payment'} style={{ flex: 1 }}>Продлить</Button>
          <Button variant="ghost" to="/payment">Сменить тариф</Button>
        </div>
      )}
    </section>
  )
}
