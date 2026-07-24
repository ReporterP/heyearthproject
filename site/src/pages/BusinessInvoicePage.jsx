import { useState } from 'react'
import { Link } from 'react-router-dom'

// Публичная форма «Получить счёт на оплату» для B2B юрлиц.
// Обращается к /api/v1/business/create-invoice (публичный endpoint).

export default function BusinessInvoicePage() {
  const [inn, setInn] = useState('')
  const [company, setCompany] = useState('')
  const [amount, setAmount] = useState('')
  const [email, setEmail] = useState('')
  const [description, setDescription] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState(null)

  async function handleSubmit(e) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const r = await fetch('/api/v1/business/create-invoice', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          inn,
          company_name: company,
          amount: parseInt(amount) || 0,
          email,
          description,
          return_url: window.location.origin + '/business',
        }),
      })
      const data = await r.json()
      if (!r.ok) throw new Error(data.error || 'Не удалось создать счёт')
      setResult(data)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const inputCls = 'w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 text-text-primary focus:border-brand/50 focus:ring-2 focus:ring-brand/20 outline-none transition placeholder:text-faint'

  if (result) {
    return (
      <section className="relative py-10 md:py-16">
        <div className="max-w-2xl mx-auto px-4 sm:px-6">
          <div className="card rounded-[22px] p-8 !border-brand/30">
            <div className="w-14 h-14 mb-4 rounded-full bg-brand/15 border border-brand/25 flex items-center justify-center">
              <svg className="w-8 h-8 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
              </svg>
            </div>
            <h1 className="serif text-2xl font-semibold mb-2">Счёт создан</h1>
            <p className="text-sm text-text-secondary mb-5">
              Счёт на <strong className="text-text-primary">{result.amount}₽</strong> сформирован для <strong className="text-text-primary">{result.company_name}</strong>. Ссылка для оплаты:
            </p>
            <div className="mb-5">
              <a href={result.confirmation_url} target="_blank" rel="noopener noreferrer" className="btn-primary">
                Перейти к оплате →
              </a>
            </div>
            <div className="p-3 rounded-lg bg-surface-2 border border-brand/10 mb-4">
              <p className="text-xs text-text-secondary mb-1">Ссылка (можете скопировать и отправить):</p>
              <p className="font-mono text-xs break-all select-all text-text-primary">{result.confirmation_url}</p>
            </div>
            <p className="text-xs text-text-secondary">
              После оплаты чек-накладная по 54-ФЗ будет автоматически отправлена на <strong className="text-text-primary">{result.email}</strong>.
            </p>
            <div className="mt-6 flex gap-3">
              <button
                onClick={() => { setResult(null); setInn(''); setCompany(''); setAmount(''); setEmail(''); setDescription('') }}
                className="btn-ghost !py-2 !px-4 !text-sm"
              >
                Создать ещё один
              </button>
              <Link to="/business" className="px-4 py-2 rounded-lg text-sm text-text-secondary hover:text-text-primary self-center">Вернуться</Link>
            </div>
          </div>
        </div>
      </section>
    )
  }

  return (
    <section className="relative py-10 md:py-16">
      <div className="max-w-2xl mx-auto px-4 sm:px-6">
        <div className="flex items-center justify-between mb-4">
          <div>
            <p className="eyebrow mb-3">Для бизнеса</p>
            <h1 className="serif text-2xl md:text-3xl font-semibold">Счёт на оплату для юрлиц</h1>
          </div>
          <Link to="/business" className="text-sm text-text-secondary hover:text-brand">← Для бизнеса</Link>
        </div>

        <p className="text-sm text-text-secondary mb-6">
          Введите реквизиты — мы выпишем счёт и отправим чек-накладную по 54-ФЗ на указанный email. Оплата проходит через защищённую систему ЮKassa.
        </p>

        <form onSubmit={handleSubmit} className="card rounded-[22px] p-6 space-y-4">
          <div>
            <label className="block text-sm text-text-secondary mb-1">ИНН <span className="text-accent">*</span></label>
            <input type="text" required value={inn} onChange={e => setInn(e.target.value.replace(/\D/g, '').slice(0, 12))} placeholder="7707083893 или 500913244106" className={`${inputCls} font-mono`} />
            <p className="text-xs text-faint mt-1">10 цифр для юрлица, 12 для ИП</p>
          </div>

          <div>
            <label className="block text-sm text-text-secondary mb-1">Название организации <span className="text-accent">*</span></label>
            <input type="text" required value={company} onChange={e => setCompany(e.target.value)} placeholder="ООО «Ромашка» или ИП Иванов И.И." className={inputCls} />
          </div>

          <div>
            <label className="block text-sm text-text-secondary mb-1">Сумма (₽) <span className="text-accent">*</span></label>
            <input type="number" min="100" required value={amount} onChange={e => setAmount(e.target.value)} placeholder="5000" className={inputCls} />
            <p className="text-xs text-faint mt-1">Минимум 100 ₽</p>
          </div>

          <div>
            <label className="block text-sm text-text-secondary mb-1">Email для чека <span className="text-accent">*</span></label>
            <input type="email" required value={email} onChange={e => setEmail(e.target.value)} placeholder="buyer@company.ru" className={inputCls} />
          </div>

          <div>
            <label className="block text-sm text-text-secondary mb-1">Описание услуг (необязательно)</label>
            <textarea rows={2} value={description} onChange={e => setDescription(e.target.value)} placeholder="HiEarth — корпоративная подписка на 10 сотрудников" className={`${inputCls} resize-none`} />
          </div>

          {error && <p className="text-sm text-danger bg-danger/10 px-3 py-2 rounded-lg">{error}</p>}

          <button type="submit" disabled={loading || !inn || !company || !amount || !email} className="btn-primary w-full !py-3 disabled:opacity-50">
            {loading ? 'Создаём…' : 'Получить счёт'}
          </button>

          <p className="text-xs text-faint text-center">
            Нажимая «Получить счёт», вы соглашаетесь с <Link to="/offer" className="text-brand hover:underline">договором-офертой</Link>
          </p>
        </form>
      </div>
    </section>
  )
}
