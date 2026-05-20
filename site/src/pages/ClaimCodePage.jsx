import { useEffect, useState } from 'react'
import { useNavigate, useParams, Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'

/*
 * /claim/:code — landing page for tenant claim codes (B2B invites).
 *
 * Flow:
 *   - If user is not logged in → redirect to /login, return to /claim/<code> after.
 *   - If logged in → POST /api/v1/payment/redeem-code to bind their account
 *     to the tenant + create a tenant_members row with the role encoded in
 *     the code prefix (tn-r-admin-..., tn-r-user-..., etc).
 *   - On success → redirect to /business/dashboard?welcome=1.
 *   - On failure (expired/used) → show error + "Back to dashboard" link.
 */

export default function ClaimCodePage() {
  const navigate = useNavigate()
  const { code } = useParams()
  const { token } = useAuth()

  const [status, setStatus] = useState('redeeming') // redeeming | done | error
  const [errorMsg, setErrorMsg] = useState('')

  useEffect(() => {
    if (!token) {
      // Persist the intended destination so /login can come back after auth.
      try { sessionStorage.setItem('claim_code_pending', code) } catch {}
      navigate('/login?next=' + encodeURIComponent('/claim/' + code))
      return
    }
    redeem()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, code])

  async function redeem() {
    try {
      const res = await fetch('/api/v1/payment/redeem-code', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ code }),
      })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(data.error || 'Не удалось активировать код')
      setStatus('done')
      // Brief pause so the user sees the confirmation, then jump in.
      setTimeout(() => navigate('/business/dashboard?welcome=1'), 1200)
    } catch (e) {
      setStatus('error')
      setErrorMsg(e.message || 'Ошибка')
    }
  }

  return (
    <section className="relative py-16 md:py-24">
      <div className="max-w-md mx-auto px-4">
        <div className="glass-card rounded-2xl p-8 text-center">
          {status === 'redeeming' && (
            <>
              <div className="w-12 h-12 mx-auto mb-5 rounded-full border-2 border-brand/30 border-t-brand animate-spin" />
              <h1 className="text-xl font-bold mb-2">Привязываем вас к компании…</h1>
              <p className="text-sm text-text-secondary font-mono break-all">{code}</p>
            </>
          )}
          {status === 'done' && (
            <>
              <div className="w-14 h-14 mx-auto mb-5 rounded-full bg-green-500/15 flex items-center justify-center">
                <svg className="w-7 h-7 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                </svg>
              </div>
              <h1 className="text-xl font-bold mb-2">Готово!</h1>
              <p className="text-sm text-text-secondary">Открываем кабинет компании…</p>
            </>
          )}
          {status === 'error' && (
            <>
              <div className="w-14 h-14 mx-auto mb-5 rounded-full bg-red-500/15 flex items-center justify-center">
                <svg className="w-7 h-7 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                </svg>
              </div>
              <h1 className="text-xl font-bold text-red-400 mb-2">Не удалось активировать код</h1>
              <p className="text-sm text-text-secondary mb-5">{errorMsg}</p>
              <Link to="/dashboard" className="inline-block px-5 py-2.5 rounded-xl bg-brand text-white text-sm font-semibold hover:bg-brand/80 transition">
                В личный кабинет
              </Link>
            </>
          )}
        </div>
      </div>
    </section>
  )
}
