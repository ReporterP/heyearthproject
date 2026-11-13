import { Link } from 'react-router-dom'
import { rub, formatDay } from '../api/wallet.js'

// The wallet on the cabinet home: the balance, «Пополнить», «История» and what the next
// auto-renewal will do.
export function renewLine(ar) {
  if (!ar) return null
  if (ar.free) return { tone: 'ok', text: 'Подписка по промокоду продлевается бесплатно' }
  if (!ar.enabled) return { tone: 'muted', text: `Автопродление выключено — подписка закончится ${formatDay(ar.renew_at)}` }
  if (ar.enough) return { tone: 'ok', text: `Автопродление: спишем ${rub(ar.price_kop)} ${formatDay(ar.renew_at)} — денег хватает` }
  return { tone: 'warn', text: `Автопродление ${formatDay(ar.renew_at)}: не хватает ${rub(ar.shortfall_kop)} — пополните кошелёк` }
}

export default function WalletCard({ wallet }) {
  if (!wallet) return null
  const line = renewLine(wallet.auto_renew)
  return (
    <div className="card rounded-[22px] p-6 reveal">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <p className="eyebrow mb-2">Кошелёк</p>
          <p className="serif text-3xl font-semibold tabular-nums">{rub(wallet.balance_kop)}</p>
        </div>
        <div className="flex gap-2">
          <Link to="/wallet#topup" className="btn-primary !py-2.5 !text-sm">Пополнить</Link>
          <Link to="/wallet" className="btn-ghost !py-2.5 !text-sm">История</Link>
        </div>
      </div>
      {line && (
        <p className={`text-sm mt-4 ${line.tone === 'warn' ? 'text-accent' : line.tone === 'ok' ? 'text-text-secondary' : 'text-faint'}`}>
          {line.text}
        </p>
      )}
    </div>
  )
}
