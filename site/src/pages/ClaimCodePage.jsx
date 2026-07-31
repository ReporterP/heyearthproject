import { useEffect, useState } from 'react'
import { useNavigate, useParams, Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'

/*
 * /claim/:code — landing page for tenant claim codes (B2B invites).
 *
 * Flow:
 *   - Not logged in → /login?redirect=/claim/<code> (LoginPage reads `redirect`).
 *   - Logged in → ASK FIRST, then POST /api/v1/payment/redeem-code. Binding an
 *     account to a company is effectively irreversible for the user, so it must
 *     not happen as a side effect of opening a link.
 *   - Success → /business/dashboard?welcome=1
 *   - Failure → error + retry / exit
 */

export default function ClaimCodePage() {
  const navigate = useNavigate()
  const { code } = useParams()
  const { token } = useAuth()

  const [status, setStatus] = useState('confirm') // confirm | redeeming | done | error
  const [errorMsg, setErrorMsg] = useState('')

  useEffect(() => {
    if (!token) {
      navigate('/login?redirect=' + encodeURIComponent('/claim/' + code))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, code])

  async function redeem() {
    setStatus('redeeming')
    setErrorMsg('')
    try {
      const res = await fetch('/api/v1/payment/redeem-code', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ code }),
      })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(data.error || 'Не удалось активировать код')
      setStatus('done')
      setTimeout(() => navigate('/business/dashboard?welcome=1'), 1200)
    } catch (e) {
      setStatus('error')
      setErrorMsg(e.message || 'Что-то пошло не так')
    }
  }

  return (
    <section className="relative py-16 md:py-24">
      <div className="max-w-md mx-auto px-4">
        <div className="card rounded-[22px] p-8 text-center">
          {status === 'confirm' && (
            <>
              <div className="w-14 h-14 mx-auto mb-5 rounded-full bg-brand/12 border border-brand/25 text-brand flex items-center justify-center">
                <svg className="w-7 h-7" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 21h16.5M4.5 3h15M5.25 3v18m13.5-18v18M9 6.75h1.5m-1.5 3h1.5m-1.5 3h1.5m3-6H15m-1.5 3H15m-1.5 3H15M9 21v-3.375c0-.621.504-1.125 1.125-1.125h3.75c.621 0 1.125.504 1.125 1.125V21" />
                </svg>
              </div>
              <h1 className="serif text-xl font-semibold mb-2">Присоединиться к компании?</h1>
              <p className="text-sm text-text-secondary mb-4">
                Ваш аккаунт будет привязан к корпоративному кабинету. Администратор компании увидит вас в списке сотрудников.
              </p>
              <div className="p-3 rounded-xl bg-white/[0.03] border border-brand/10 mb-6">
                <p className="text-[11px] text-faint uppercase tracking-wider mb-1">Код приглашения</p>
                <p className="text-sm font-mono break-all text-text-primary">{code}</p>
              </div>
              <div className="flex gap-3">
                <Link to="/dashboard" className="btn-ghost flex-1 !py-2.5 !text-sm">Не сейчас</Link>
                <button onClick={redeem} className="btn-primary flex-1 !py-2.5 !text-sm">Присоединиться</button>
              </div>
            </>
          )}

          {status === 'redeeming' && (
            <>
              <div className="w-12 h-12 mx-auto mb-5 rounded-full border-2 border-brand/25 border-t-brand animate-spin" />
              <h1 className="serif text-xl font-semibold mb-2">Привязываем вас к компании…</h1>
              <p className="text-sm text-text-secondary font-mono break-all">{code}</p>
            </>
          )}

          {status === 'done' && (
            <>
              <div className="w-14 h-14 mx-auto mb-5 rounded-full bg-brand/15 border border-brand/25 flex items-center justify-center">
                <svg className="w-7 h-7 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                </svg>
              </div>
              <h1 className="serif text-xl font-semibold mb-2">Готово!</h1>
              <p className="text-sm text-text-secondary">Открываем кабинет компании…</p>
            </>
          )}

          {status === 'error' && (
            <>
              <div className="w-14 h-14 mx-auto mb-5 rounded-full bg-danger/15 flex items-center justify-center">
                <svg className="w-7 h-7 text-danger" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                </svg>
              </div>
              <h1 className="serif text-xl font-semibold text-danger mb-2">Не удалось активировать код</h1>
              <p className="text-sm text-text-secondary mb-2">{errorMsg}</p>
              <p className="text-xs text-faint mb-6">Код мог истечь или быть использован. Попросите администратора компании выслать новый.</p>
              <div className="flex gap-3">
                <Link to="/dashboard" className="btn-ghost flex-1 !py-2.5 !text-sm">В кабинет</Link>
                <button onClick={redeem} className="btn-primary flex-1 !py-2.5 !text-sm">Повторить</button>
              </div>
            </>
          )}
        </div>
      </div>
    </section>
  )
}
