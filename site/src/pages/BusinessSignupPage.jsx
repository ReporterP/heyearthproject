import { useState, useMemo } from 'react'
import { createTenant } from '../api/tenant.js'

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

  const monthlyEstimate = useMemo(() => {
    if (specs.unlimited) return 20000
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
      await createTenant({ email, companyName, specs })
      setSubmitted(true)
    } catch (e) {
      setError(e.message || 'Ошибка. Попробуйте позже.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="py-24">
      <div className="max-w-2xl mx-auto px-6">
        <h1 className="text-3xl font-bold text-center mb-2">Подключение бизнес-аккаунта</h1>
        <p className="text-text-secondary text-center mb-12">Без предоплаты на сайте. Оплата — после входа в панель управления.</p>

        {submitted ? (
          <div className="text-center p-10 rounded-2xl bg-surface-card border border-brand/30">
            <div className="text-5xl mb-4">&#9989;</div>
            <h2 className="text-2xl font-bold mb-2">Готово!</h2>
            <p className="text-text-secondary">
              Данные для входа в панель управления отправлены на <span className="text-text-primary font-medium">{email}</span>.
              Пополните кошелёк и активируйте выбранные параметры.
            </p>
          </div>
        ) : (
          <>
            {/* Step indicators */}
            <div className="flex items-center justify-center gap-4 mb-10">
              {[1, 2].map(s => (
                <div key={s} className="flex items-center gap-2">
                  <div className={`w-8 h-8 rounded-full flex items-center justify-center text-sm font-bold transition ${
                    step >= s ? 'bg-brand text-white' : 'bg-surface-card border border-white/10 text-text-secondary'
                  }`}>
                    {s}
                  </div>
                  <span className={`text-sm ${step >= s ? 'text-text-primary' : 'text-text-secondary'}`}>
                    {s === 1 ? 'Параметры' : 'Контакт'}
                  </span>
                  {s < 2 && <div className="w-12 h-px bg-white/10" />}
                </div>
              ))}
            </div>

            {/* Step 1: Specs */}
            {step === 1 && (
              <div className="space-y-6">
                {!specs.unlimited && (
                  <>
                    <div>
                      <label className="block text-sm text-text-secondary mb-2">Количество пользователей</label>
                      <input
                        type="number" min="1" value={specs.users}
                        onChange={e => updateSpec('users', parseInt(e.target.value) || 1)}
                        className="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
                      />
                    </div>
                    <div>
                      <label className="block text-sm text-text-secondary mb-2">Своих серверов (5 включены)</label>
                      <input
                        type="number" min="0" value={specs.ownServers}
                        onChange={e => updateSpec('ownServers', parseInt(e.target.value) || 0)}
                        className="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
                      />
                    </div>
                    <label className="flex items-center gap-3 cursor-pointer">
                      <input type="checkbox" checked={specs.useProviderServers} onChange={e => updateSpec('useProviderServers', e.target.checked)} className="accent-brand w-5 h-5" />
                      <span>Использовать наши серверы (+1 000 &#8381;/мес)</span>
                    </label>
                    <label className="flex items-center gap-3 cursor-pointer">
                      <input type="checkbox" checked={specs.premiumEnabled} onChange={e => updateSpec('premiumEnabled', e.target.checked)} className="accent-brand w-5 h-5" />
                      <span>Премиум для пользователей (+50 &#8381;/мес за каждого)</span>
                    </label>
                  </>
                )}

                <div className="p-6 rounded-xl bg-gradient-to-r from-brand/10 to-transparent border border-brand/20">
                  <div className="flex justify-between items-baseline">
                    <span className="text-text-secondary">Ориентировочная стоимость</span>
                    <span className="text-2xl font-extrabold">
                      {monthlyEstimate.toLocaleString('ru-RU')} &#8381;
                      <span className="text-sm font-normal text-text-secondary"> / мес</span>
                    </span>
                  </div>
                </div>

                <button className="w-full py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition" onClick={() => setStep(2)}>
                  Далее
                </button>
              </div>
            )}

            {/* Step 2: Contact */}
            {step === 2 && (
              <div className="space-y-6">
                <div>
                  <label className="block text-sm text-text-secondary mb-2">Email *</label>
                  <input type="email" value={email} onChange={e => setEmail(e.target.value)} placeholder="you@company.com"
                    className="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition" />
                </div>
                <div>
                  <label className="block text-sm text-text-secondary mb-2">Название компании</label>
                  <input type="text" value={companyName} onChange={e => setCompanyName(e.target.value)} placeholder={'\u041E\u041E\u041E \u00AB\u041A\u043E\u043C\u043F\u0430\u043D\u0438\u044F\u00BB'}
                    className="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition" />
                </div>

                <div className="p-6 rounded-xl bg-surface-card border border-white/5 text-sm text-text-secondary">
                  <p className="mb-1"><span className="text-text-primary font-medium">Итого:</span> {monthlyEstimate.toLocaleString('ru-RU')} &#8381; / мес</p>
                  <p>После отправки вы получите доступ в панель управления. Оплата — через пополнение кошелька в панели.</p>
                </div>

                {error && <p className="text-red-400 text-sm">{error}</p>}

                <div className="flex gap-3">
                  <button className="flex-1 py-3 rounded-xl border border-white/10 hover:border-white/20 text-text-secondary transition" onClick={() => setStep(1)}>
                    Назад
                  </button>
                  <button className="flex-1 py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition disabled:opacity-50" disabled={submitting} onClick={submit}>
                    {submitting ? 'Отправка...' : 'Получить доступ'}
                  </button>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </section>
  )
}
