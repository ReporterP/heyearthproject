import { useEffect, useRef, useState } from 'react'
import { Navigate, useNavigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import MiniPage from '../components/MiniPage.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import Button from '../components/Button.jsx'
import CopyField from '../components/CopyField.jsx'
import { Progress } from '../components/Controls.jsx'
import usePageMeta from '../hooks/usePageMeta.js'

// Код приглашения /claim/:code (spec 10 §10.5, artboard «Приглашение в компанию»). A guest goes to the
// login first (the login page explains why). Binding an account to a company is not undone by the
// person, so it happens only after «Присоединиться» — never just because the link was opened.
// POST /api/v1/payment/redeem-code → «Готово!» → /business/dashboard?welcome=1 after 1.5 s.
export default function ClaimCodePage() {
  const { code = '' } = useParams()
  const { token, isLoggedIn } = useAuth()
  const navigate = useNavigate()
  usePageMeta('Приглашение в компанию — HiEarth', { noindex: true })
  const [state, setState] = useState('confirm') // confirm | busy | joined | failed | temp
  const [noSeats, setNoSeats] = useState(false) // «failed» because the company is full
  const inFlight = useRef(false)

  useEffect(() => {
    if (state !== 'joined') return undefined
    const t = setTimeout(() => navigate('/business/dashboard?welcome=1', { replace: true }), 1500)
    return () => clearTimeout(t)
  }, [state, navigate])

  if (!isLoggedIn) return <Navigate to={`/login?redirect=${encodeURIComponent(`/claim/${code}`)}`} replace />

  async function join() {
    if (inFlight.current) return
    inFlight.current = true
    setState('busy')
    try {
      const r = await fetch('/api/v1/payment/redeem-code', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ code }),
      })
      // the session ended: back through the login, the code is kept in `redirect`
      if (r.status === 401) { navigate(`/login?redirect=${encodeURIComponent(`/claim/${code}`)}`, { replace: true }); return }
      // a refused code (400/404/409) is «истёк или использован»; anything else is a temporary failure
      const refused = !r.ok && r.status >= 400 && r.status < 500 && r.status !== 429
      const why = refused ? String((await r.json().catch(() => ({}))).error || '') : ''
      setNoSeats(why === 'no seats left')
      setState(r.ok ? 'joined' : refused ? 'failed' : 'temp')
    } catch {
      setState('temp')
    } finally {
      inFlight.current = false
    }
  }

  return (
    <MiniPage label="Приглашение в компанию">
      {(state === 'confirm' || state === 'busy') && (
        <div key="ask" className="swap stack" style={{ alignItems: 'center', gap: 14, width: '100%' }}>
          <Hedgehog mood="idle" size={110} />
          <h1 className="t-h2">Присоединиться к&nbsp;компании?</h1>
          <p className="muted t-sm">Ваш аккаунт будет привязан к корпоративному кабинету. Администратор компании увидит вас в списке сотрудников. Доступ оплачивает компания.</p>
          <CopyField value={code} display={`Код приглашения: ${code}`} label="Код приглашения" style={{ width: '100%' }} />
          <div className="row gap-2 max-sm:flex-col-reverse" style={{ width: "100%" }}>
            <Button variant="ghost" to="/dashboard" className="sm:flex-1 max-sm:w-full" disabled={state === "busy"}>Не сейчас</Button>
            <Button variant="primary" onClick={join} loading={state === "busy"} className="sm:flex-1 max-sm:w-full">Присоединиться</Button>
          </div>
        </div>
      )}
      {state === 'joined' && (
        <div key="joined" className="swap stack" style={{ alignItems: 'center', gap: 14, width: '100%' }}>
          <Hedgehog mood="happy" size={110} />
          <h1 className="t-h2">Готово!</h1>
          <p className="muted t-sm" role="status">Открываем кабинет компании…</p>
          <div style={{ width: '100%' }}><Progress value={100} label="Открываем кабинет компании" /></div>
        </div>
      )}
      {(state === 'failed' || state === 'temp') && (
        <div key={state} className="swap stack" style={{ alignItems: 'center', gap: 14, width: '100%' }}>
          <Hedgehog mood="worried" size={110} />
          <h1 className="t-h2">Не удалось активировать код</h1>
          <p className="muted t-sm" role="alert">{state === 'temp' ? 'Сервис временно недоступен. Попробуйте ещё раз через минуту.' : noSeats ? 'В компании не осталось мест. Попросите администратора освободить место или увеличить лимит.' : 'Код мог истечь или быть использован. Попросите администратора компании выслать новый.'}</p>
          <div className="row wrap gap-2" style={{ justifyContent: 'center' }}>
            <Button variant="secondary" to="/dashboard">В кабинет</Button>
            <Button variant="ghost" onClick={join}>Повторить</Button>
          </div>
        </div>
      )}
    </MiniPage>
  )
}
