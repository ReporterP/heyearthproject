import NarrowHeader from './NarrowHeader.jsx'
import Steps from './Steps.jsx'
import useAccount from '../account/useAccount.js'
import { kopNum } from '../lib/format.js'

// The header of the payment path (spec 09): `back` on the left, «Тариф → Оформление → Оплата» in the
// middle, «Кошелёк N ₽» on the right (mono). No side menu on these pages.
export default function PayHeader({ back, step, onStep, planSlug }) {
  const { data } = useAccount(['wallet'], { optional: ['wallet'] })
  const bal = data?.wallet ? kopNum(data.wallet.balance_kop) : null
  const items = [
    { label: 'Тариф', to: '/payment', onClick: onStep ? () => onStep(0) : undefined },
    { label: 'Оформление', to: planSlug ? `/payment?plan=${encodeURIComponent(planSlug)}` : undefined, onClick: onStep ? () => onStep(1) : undefined },
    { label: 'Оплата' },
  ].map((it) => (it.onClick ? { label: it.label, onClick: it.onClick } : it))
  return (
    <NarrowHeader
      back={back}
      center={<Steps items={items} current={step} />}
      right={bal === null ? null : <span className="row gap-2 t-sm muted max-sm:hidden">Кошелёк <b className="mono" style={{ color: 'var(--fg)' }}>{bal.toLocaleString('ru-RU')}&nbsp;₽</b></span>}
    />
  )
}
