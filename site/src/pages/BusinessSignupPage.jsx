import { useState, useMemo } from 'react'
import { Link } from 'react-router-dom'
import { createTenant } from '../api/tenant.js'

const Arrow = ({ className = 'w-4 h-4' }) => (
  <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M13 6l6 6-6 6" /></svg>
)

export default function BusinessSignupPage() {
  const [step, setStep] = useState(1)
  const [specs, setSpecs] = useState({
    users: 10, ownServers: 0, useProviderServers: true, premiumEnabled: false, unlimited: false,
  })
  const [email, setEmail] = useState('')
  const [companyName, setCompanyName] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [error, setError] = useState('')
  const [signupResult, setSignupResult] = useState(null)

  // Client-side estimate only. The signup endpoint records users/servers; the
  // premium option is confirmed by a manager, so this is never presented as a
  // binding total (see the note under the figure).
  const monthlyEstimate = useMemo(() => {
    let total = specs.users * 250
    if (specs.useProviderServers) total += 1000
    const extraServers = Math.max(0, specs.ownServers - 5)
    total += extraServers * 100
    if (specs.premiumEnabled) total += specs.users * 50
    return total
  }, [specs])

  function updateSpec(key, value) {
    setSpecs(prev => ({ ...prev, [key]: value }))
  }

  async function submit() {
    if (!email.trim()) { setError('Укажите email'); return }
    if (!companyName.trim()) { setError('Укажите название компании'); return }
    setSubmitting(true); setError('')
    try {
      const res = await createTenant({ email, companyName, specs })
      setSignupResult(res || null)
      setSubmitted(true)
    } catch (e) {
      setError(e.message || 'Что-то пошло не так. Попробуйте позже.')
    } finally {
      setSubmitting(false)
    }
  }

  const inputCls = 'w-full px-4 py-3.5 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:bg-white/[0.06] focus:outline-none focus:ring-2 focus:ring-brand/25 transition-all duration-300 text-sm placeholder:text-faint'

  return (
    <section className="relative py-16 md:py-24 overflow-hidden">
      <div className="pointer-events-none absolute top-1/3 -left-24 w-80 h-80 rounded-full bg-brand/8 blur-[110px]" />
      <div className="pointer-events-none absolute bottom-1/3 -right-24 w-80 h-80 rounded-full bg-accent/8 blur-[110px]" />

      <div className="relative max-w-2xl mx-auto px-6">
        <div className="text-center mb-10 animate-fade-in">
          <p className="eyebrow mb-5">Бизнес-аккаунт</p>
          <h1 className="serif text-2xl md:text-3xl font-semibold mb-2">Подключение бизнес-аккаунта</h1>
          <p className="text-text-secondary text-sm">Без предоплаты на сайте. Оплата — после входа в панель управления.</p>
        </div>

        {submitted ? (
          <div className="glass-strong rounded-[26px] p-8 md:p-10 text-center animate-fade-in-scale">
            <div className="w-16 h-16 rounded-full bg-brand/15 border border-brand/25 flex items-center justify-center mx-auto mb-6">
              <svg className="w-8 h-8 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
            </div>
            <h2 className="serif text-2xl font-semibold mb-2">Аккаунт компании создан</h2>
            <p className="text-text-secondary leading-relaxed mb-6">
              Письмо с инструкциями отправлено на <span className="text-text-primary font-medium">{email}</span>. Чтобы активировать аккаунт — войдите в кабинет компании и пополните кошелёк.
            </p>

            {signupResult?.claim_url && (
              <div className="p-4 rounded-xl bg-white/[0.04] border border-brand/12 text-left mb-6">
                <p className="text-[11px] text-faint uppercase tracking-wider mb-2">Ссылка для активации</p>
                <code className="block font-mono text-xs break-all select-all text-text-primary">{signupResult.claim_url}</code>
                <p className="text-[11px] text-text-secondary mt-2">Зарегистрируйтесь на сайте, затем откройте эту ссылку — мы привяжем ваш аккаунт к компании.</p>
              </div>
            )}

            <div className="flex flex-col sm:flex-row gap-3 justify-center">
              <Link to="/login" className="btn-primary !py-2.5 !text-sm">Войти в аккаунт</Link>
              <Link to="/register" className="btn-ghost !py-2.5 !text-sm">Зарегистрироваться</Link>
            </div>
          </div>
        ) : (
          <>
            {/* Step indicators */}
            <div className="flex items-center justify-center gap-3 mb-10 animate-fade-in">
              {[{ num: 1, label: 'Параметры' }, { num: 2, label: 'Контакт' }].map((s, i) => (
                <div key={s.num} className="flex items-center gap-3">
                  <button
                    className={`flex items-center gap-2.5 px-4 py-2 rounded-xl transition-all duration-300 ${step >= s.num ? 'glass-strong !border-brand/30' : 'glass'}`}
                    onClick={() => s.num < step && setStep(s.num)}
                  >
                    <div className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-bold transition-all duration-300 ${
                      step > s.num ? 'bg-brand/20 text-brand' : step === s.num ? 'bg-brand text-[#04231a]' : 'bg-white/5 text-text-secondary'
                    }`}>
                      {step > s.num ? (
                        <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={3}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
                      ) : s.num}
                    </div>
                    <span className={`text-sm font-medium transition-colors ${step >= s.num ? 'text-text-primary' : 'text-text-secondary'}`}>{s.label}</span>
                  </button>
                  {i < 1 && <div className={`w-8 h-0.5 rounded-full transition-colors duration-300 ${step > 1 ? 'bg-brand/40' : 'bg-white/10'}`} />}
                </div>
              ))}
            </div>

            {/* Step 1: Specs */}
            {step === 1 && (
              <div className="glass-strong rounded-[26px] p-6 md:p-8 animate-fade-in-scale">
                <div className="space-y-6">
                  {(
                    <>
                      <div className="group">
                        <label className="flex items-center gap-2 text-xs font-medium text-text-secondary mb-2 group-focus-within:text-brand transition-colors">
                          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M15 19.128a9.38 9.38 0 002.625.372 9.337 9.337 0 004.121-.952 4.125 4.125 0 00-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128v.106A12.318 12.318 0 018.624 21c-2.331 0-4.512-.645-6.374-1.766l-.001-.109a6.375 6.375 0 0111.964-3.07M12 6.375a3.375 3.375 0 11-6.75 0 3.375 3.375 0 016.75 0zm8.25 2.25a2.625 2.625 0 11-5.25 0 2.625 2.625 0 015.25 0z" /></svg>
                          Количество пользователей
                        </label>
                        <input type="number" min="1" value={specs.users} onChange={e => updateSpec('users', parseInt(e.target.value) || 1)} className={inputCls} />
                      </div>

                      <div className="group">
                        <label className="flex items-center gap-2 text-xs font-medium text-text-secondary mb-2 group-focus-within:text-brand transition-colors">
                          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z" /></svg>
                          Своих серверов (5 включены)
                        </label>
                        <input type="number" min="0" value={specs.ownServers} onChange={e => updateSpec('ownServers', parseInt(e.target.value) || 0)} className={inputCls} />
                      </div>

                      <div className="space-y-3">
                        <label className="group flex items-center gap-3 p-4 rounded-xl card card-hover cursor-pointer">
                          <div className="relative flex-shrink-0">
                            <input type="checkbox" checked={specs.useProviderServers} onChange={e => updateSpec('useProviderServers', e.target.checked)} className="sr-only peer" />
                            <div className="w-10 h-6 rounded-full bg-white/10 peer-checked:bg-brand transition-colors duration-300" />
                            <div className="absolute top-1 left-1 w-4 h-4 rounded-full bg-white transition-transform duration-300 peer-checked:translate-x-4" />
                          </div>
                          <div>
                            <span className="text-sm font-medium">Использовать наши серверы</span>
                            <span className="block text-xs text-text-secondary mt-0.5">+1 000 ₽/мес</span>
                          </div>
                        </label>

                        <label className="group flex items-center gap-3 p-4 rounded-xl card card-hover cursor-pointer">
                          <div className="relative flex-shrink-0">
                            <input type="checkbox" checked={specs.premiumEnabled} onChange={e => updateSpec('premiumEnabled', e.target.checked)} className="sr-only peer" />
                            <div className="w-10 h-6 rounded-full bg-white/10 peer-checked:bg-brand transition-colors duration-300" />
                            <div className="absolute top-1 left-1 w-4 h-4 rounded-full bg-white transition-transform duration-300 peer-checked:translate-x-4" />
                          </div>
                          <div>
                            <span className="text-sm font-medium">Премиум для пользователей</span>
                            <span className="block text-xs text-text-secondary mt-0.5">+50 ₽/мес за каждого</span>
                          </div>
                        </label>
                      </div>
                    </>
                  )}

                  <div className="relative p-6 rounded-2xl glass border border-brand/20 overflow-hidden">
                    <div className="absolute top-0 right-0 w-24 h-24 bg-gradient-to-bl from-brand/10 to-transparent rounded-bl-[80px] pointer-events-none" />
                    <div className="relative flex items-baseline justify-between">
                      <span className="text-sm text-text-secondary">Предварительная оценка</span>
                      <div className="text-right">
                        <span className="serif text-3xl font-semibold tracking-tight tabular-nums">≈ {monthlyEstimate.toLocaleString('ru-RU')}</span>
                        <span className="text-sm text-text-secondary ml-1">₽ / мес</span>
                      </div>
                    </div>
                    <p className="relative text-[11px] text-faint mt-2">
                      Расчёт по базовым ставкам. Итоговый тариф подтверждается менеджером после регистрации — списаний до этого не будет.
                    </p>
                  </div>

                  <button className="btn-primary w-full !py-3.5" onClick={() => setStep(2)}>Далее <Arrow /></button>
                </div>
              </div>
            )}

            {/* Step 2: Contact */}
            {step === 2 && (
              <div className="glass-strong rounded-[26px] p-6 md:p-8 animate-fade-in-scale">
                <div className="space-y-5">
                  <div className="group">
                    <label className="flex items-center gap-2 text-xs font-medium text-text-secondary mb-2 group-focus-within:text-brand transition-colors">
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M21.75 6.75v10.5a2.25 2.25 0 01-2.25 2.25h-15a2.25 2.25 0 01-2.25-2.25V6.75m19.5 0A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25m19.5 0v.243a2.25 2.25 0 01-1.07 1.916l-7.5 4.615a2.25 2.25 0 01-2.36 0L3.32 8.91a2.25 2.25 0 01-1.07-1.916V6.75" /></svg>
                      Email <span className="text-accent">*</span>
                    </label>
                    <input type="email" value={email} onChange={e => setEmail(e.target.value)} placeholder="you@company.com" className={inputCls} />
                  </div>

                  <div className="group">
                    <label className="flex items-center gap-2 text-xs font-medium text-text-secondary mb-2 group-focus-within:text-brand transition-colors">
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M3.75 21h16.5M4.5 3h15M5.25 3v18m13.5-18v18M9 6.75h1.5m-1.5 3h1.5m-1.5 3h1.5m3-6H15m-1.5 3H15m-1.5 3H15M9 21v-3.375c0-.621.504-1.125 1.125-1.125h3.75c.621 0 1.125.504 1.125 1.125V21" /></svg>
                      Название компании <span className="text-accent">*</span>
                    </label>
                    <input type="text" value={companyName} onChange={e => setCompanyName(e.target.value)} placeholder="ООО «Компания»" className={inputCls} />
                  </div>

                  <div className="p-5 rounded-xl glass border border-brand/10">
                    <div className="flex items-baseline justify-between mb-2">
                      <span className="text-sm font-medium">Предварительно</span>
                      <span className="font-semibold tabular-nums">≈ {monthlyEstimate.toLocaleString('ru-RU')} ₽ / мес</span>
                    </div>
                    <ul className="text-xs text-text-secondary leading-relaxed space-y-1">
                      <li>· {specs.users} польз. × 250 ₽{specs.premiumEnabled ? ' + премиум 50 ₽/чел.' : ''}</li>
                      {specs.useProviderServers && <li>· наши серверы — 1 000 ₽</li>}
                      {specs.ownServers > 5 && <li>· доп. серверы: {specs.ownServers - 5} × 100 ₽</li>}
                      <li className="text-faint pt-1">Оплата — через пополнение кошелька в панели. Сейчас ничего не спишется.</li>
                    </ul>
                  </div>

                  {error && (
                    <div className="flex items-start gap-2 p-3 rounded-xl bg-danger/10 border border-danger/20 animate-fade-in">
                      <svg className="w-5 h-5 text-danger flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" /></svg>
                      <p className="text-danger text-sm">{error}</p>
                    </div>
                  )}

                  <div className="flex gap-3">
                    <button className="btn-ghost flex-1 !py-3.5" onClick={() => setStep(1)}>
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5L3 12m0 0l7.5-7.5M3 12h18" /></svg>
                      Назад
                    </button>
                    <button className="btn-primary flex-1 !py-3.5 disabled:opacity-50" disabled={submitting} onClick={submit}>
                      {submitting ? (
                        <>
                          <svg className="w-4 h-4 animate-spin" viewBox="0 0 24 24" fill="none">
                            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                          </svg>
                          Отправка…
                        </>
                      ) : 'Получить доступ'}
                    </button>
                  </div>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </section>
  )
}
