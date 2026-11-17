import { useCallback, useEffect, useState } from 'react'
import { Navigate, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { apiFetch } from '../api/http.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { invalidateAccount } from '../account/useAccount.js'
import { isMiniApp, openInvoice } from '../lib/telegram.js'
import AppShell, { PageHead } from './AppShell.jsx'
import Alert from './Alert.jsx'
import Button from './Button.jsx'
import EmptyState from './EmptyState.jsx'
import PlanCard from './PlanCard.jsx'
import { SkeletonCard, useDelayed } from './Skeleton.jsx'
import { useToast } from './Toast.jsx'
import usePageMeta from '../hooks/usePageMeta.js'

// Оплата звёздами (spec 12 §12.3): inside Telegram a plan is paid only in Telegram Stars (Bot
// Platform Developer Terms 6.2). Nothing here links to the site's payments.
export function StarsPayPage() {
  usePageMeta('Оплата — HiEarth', { noindex: true })
  const { token } = useAuth()
  const toast = useToast()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [data, setData] = useState(null)
  const [err, setErr] = useState(null)
  const [busy, setBusy] = useState(0)
  const [payErr, setPayErr] = useState('')
  const showSkel = useDelayed(!data && !err, 300)

  const load = useCallback(() => {
    setErr(null)
    apiFetch('/api/v1/stars/plans', { token }).then(setData).catch((e) => { if (e.kind !== 'auth') setErr(e) })
  }, [token])
  useEffect(() => { load() }, [load])

  async function pay(o) {
    if (busy) return
    setBusy(o.id)
    setPayErr('')
    try {
      const r = await apiFetch('/api/v1/stars/invoice', { token, method: 'POST', body: { plan_id: o.id } })
      const status = await openInvoice(r.url)
      if (status === 'paid') {
        toast.ok('Подписка оплачена')
        invalidateAccount()
        navigate('/dashboard', { replace: true })
      } else if (status === 'failed') {
        setPayErr('Оплата не прошла — звёзды не списаны. Попробуйте ещё раз.')
      }
    } catch (e) {
      setPayErr(e.message)
    } finally {
      setBusy(0)
    }
  }

  const plans = data?.plans || []
  const wanted = params.get('plan')
  const pick = plans.find((p) => p.slug === wanted && p.allowed) || plans.find((p) => p.current && p.allowed) || plans.find((p) => p.allowed)

  let body
  if (err && !data) {
    body = <EmptyState mood="worried" title="Не удалось загрузить тарифы." actions={<Button variant="secondary" onClick={load}>Повторить</Button>}>Обновите страницу.</EmptyState>
  } else if (!data) {
    body = <div className="grid g-3" aria-busy="true">{showSkel && [0, 1, 2].map((i) => <SkeletonCard key={i} lines={4} />)}</div>
  } else if (!data.enabled || !plans.length) {
    body = (
      <EmptyState mood="idle" title="Оплата в Telegram скоро появится" actions={<Button variant="secondary" to="/dashboard">В кабинет</Button>}>
        Подписка с автопродлением продлится с кошелька сама, если на нём хватает денег.
      </EmptyState>
    )
  } else {
    body = (
      <div className="stack gap-4">
        {payErr && <Alert tone="bad" title="Не получилось." rise>{payErr}</Alert>}
        <div className="grid g-3" style={{ alignItems: 'stretch' }}>
          {plans.map((o) => (
            <PlanCard key={o.id} plan={o} current={o.current} featured={pick?.id === o.id} primary={pick?.id === o.id}
              priceLabel={`${o.stars} ⭐`}
              cta={o.current ? `Продлить за ${o.stars} ⭐` : `Оплатить ${o.stars} ⭐`}
              onClick={() => pay(o)} loading={busy === o.id} disabled={!o.allowed || (!!busy && busy !== o.id)} disabledNote={o.reason} />
          ))}
        </div>
        <p className="t-xs subtle">Звёзды покупаются в Telegram. Если подписка уже есть, дни добавятся к ней. Вопросы об оплате — команда /paysupport в боте.</p>
      </div>
    )
  }

  return (
    <AppShell>
      <PageHead title="Оплата звёздами" lead="В Telegram подписку оплачивают звёздами — без карты и перехода на другие сайты." />
      {body}
    </AppShell>
  )
}

// MiniAppPay renders the site's payment page, and inside Telegram the Stars page instead
// (/checkout/:id there goes to /payment).
export default function MiniAppPay({ children }) {
  const { pathname } = useLocation()
  if (!isMiniApp()) return children
  if (pathname.startsWith('/checkout')) return <Navigate to="/payment" replace />
  return <StarsPayPage />
}
