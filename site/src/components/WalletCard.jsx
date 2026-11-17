import { Link } from 'react-router-dom'
import Button from './Button.jsx'
import Flap from './Flap.jsx'
import { kop, kopNum } from '../lib/format.js'

// «Кошелёк» on the overview (spec 08 §8.1 item 12): balance as flap, the shortfall before the
// renewal in warn colour, «Пополнить» (always secondary: the screen's primary is «Добавить в Happ»).
//   <WalletCard wallet note? phone />  note — the text under the balance in the «none»/«expired» states
export default function WalletCard({ wallet, note, noteTone, phone, history = true }) {
  const bal = kopNum(wallet?.balance_kop)
  const ar = wallet?.auto_renew
  const short = ar && ar.enabled && !ar.free && !ar.enough && ar.shortfall_kop > 0 ? ar.shortfall_kop : 0
  const line = note
    ? { text: note, tone: noteTone || 'subtle' }
    : short ? { text: `До продления не хватает ${kop(short)}`, tone: 'warn' } : null
  if (phone) {
    return (
      <section className="card row between gap-3" aria-labelledby="wc-t">
        <span>
          <span id="wc-t" className="card__title" style={{ display: 'block', marginBottom: 6 }}>Кошелёк</span>
          <Flap value={bal} suffix="₽" size={30} tone={line?.tone === 'warn' ? 'warn' : undefined} label={`${bal} рублей на кошельке`} />
          {line && <span className={`t-xs ${line.tone}`} style={{ display: 'block', marginTop: 6 }}>{line.text}</span>}
        </span>
        <Button variant="secondary" to="/wallet">Пополнить</Button>
      </section>
    )
  }
  return (
    <section className="card">
      <div className="card__head">
        <h2 className="card__title">Кошелёк</h2>
        {history && <Link to="/wallet#history" className="t-sm link">История</Link>}
      </div>
      <Flap value={bal} suffix="₽" size={46} tone={line?.tone === 'warn' ? 'warn' : undefined} label={`${bal} рублей на кошельке`} />
      {line && <p className={`t-sm ${line.tone}`} style={{ marginTop: 10 }}>{line.text}</p>}
      <Button variant="secondary" block to="/wallet" style={{ marginTop: 18 }}>Пополнить</Button>
    </section>
  )
}
