import { useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import NarrowHeader from '../components/NarrowHeader.jsx'
import Alert from '../components/Alert.jsx'
import Badge from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import CopyField from '../components/CopyField.jsx'
import Field, { Input } from '../components/Field.jsx'
import Flap from '../components/Flap.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { group } from '../lib/format.js'

// Счёт для юрлиц /business/invoice (spec 10 §10.3, artboard «Счёт для юрлиц»): public form →
// POST /api/v1/business/create-invoice → the invoice with its payment link (copy / open).
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export default function BusinessInvoicePage() {
  const narrow = useMedia('(max-width: 1024px)')
  usePageMeta('Счёт для юрлиц — HiEarth')
  const [inn, setInn] = useState('')
  const [amount, setAmount] = useState('')
  const [amountFocus, setAmountFocus] = useState(false)
  const [company, setCompany] = useState('')
  const [email, setEmail] = useState('')
  const [desc, setDesc] = useState('')
  const [touched, setTouched] = useState({})
  const [tried, setTried] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [srvField, setSrvField] = useState(null) // { field, text }
  const [result, setResult] = useState(null)
  const inFlight = useRef(false)

  const amountNum = parseInt(amount, 10) || 0
  const errs = {
    inn: !inn ? 'Укажите ИНН' : !(inn.length === 10 || inn.length === 12) ? 'ИНН должен содержать 10 цифр (юрлицо) или 12 (ИП)' : null,
    amount: !amount ? 'Укажите сумму' : amountNum < 100 ? 'Минимум 100 ₽' : null,
    company: !company.trim() ? 'Укажите название организации' : null,
    email: !email.trim() ? 'Укажите email' : !EMAIL_RE.test(email.trim()) ? 'Проверьте адрес почты — в нём должна быть «@» и домен' : null,
  }
  const show = (k) => ((tried || touched[k]) && errs[k]) || (srvField?.field === k && srvField.text) || null
  const blur = (k) => () => setTouched((t) => ({ ...t, [k]: true }))

  async function submit(e) {
    e.preventDefault()
    setTried(true)
    setSrvField(null)
    if (Object.values(errs).some(Boolean) || inFlight.current) return
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      const r = await fetch('/api/v1/business/create-invoice', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ inn, company_name: company.trim(), amount: amountNum, email: email.trim(), description: desc.trim(), return_url: `${window.location.origin}/business` }),
      })
      const data = await r.json().catch(() => ({}))
      if (!r.ok) {
        const m = String(data.error || '')
        if (/ИНН/i.test(m)) setSrvField({ field: 'inn', text: /контрольн/i.test(m) ? 'ИНН не прошёл проверку — проверьте цифры' : 'ИНН должен содержать 10 цифр (юрлицо) или 12 (ИП)' })
        else if (/amount|сумм/i.test(m)) setSrvField({ field: 'amount', text: /1 000 000/.test(m) ? 'Сумма больше 1 000 000 ₽ — напишите нам, выставим счёт вручную' : 'Минимум 100 ₽' })
        else if (r.status === 429) setErr('Слишком много счетов подряд — попробуйте через час.')
        else setErr(/[а-я]/i.test(m) ? m : 'Не удалось создать счёт — попробуйте ещё раз.')
        return
      }
      setResult(data)
    } catch {
      setErr('Нет связи с сервером — проверьте интернет и повторите.')
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  function again() {
    setResult(null); setInn(''); setAmount(''); setCompany(''); setEmail(''); setDesc(''); setTried(false); setTouched({})
  }

  const form = (
    <form className="card stack gap-4" style={{ padding: 28 }} onSubmit={submit} noValidate>
      <div className="grid" style={{ gridTemplateColumns: narrow ? '1fr' : '1fr 1fr', gap: 12 }}>
        <Field label="ИНН" error={show('inn')}>
          <Input mono inputMode="numeric" value={inn} onBlur={blur('inn')} onChange={(e) => { setInn(e.target.value.replace(/\D/g, '').slice(0, 12)); setSrvField(null) }} />
        </Field>
        <Field label="Сумма, ₽" hint="минимум 100 ₽" error={show('amount')}>
          <Input mono inputMode="numeric" value={amountFocus || !amount ? amount : group(amountNum)} onFocus={() => setAmountFocus(true)} onBlur={() => { setAmountFocus(false); blur('amount')() }}
            onChange={(e) => { setAmount(e.target.value.replace(/\D/g, '').slice(0, 7)); setSrvField(null) }} />
        </Field>
      </div>
      <Field label="Название организации" error={show('company')}>
        <Input value={company} placeholder="ООО «Ромашка» или ИП Иванов И.И." autoComplete="organization" onBlur={blur('company')} onChange={(e) => setCompany(e.target.value)} />
      </Field>
      <Field label="Email для чека" error={show('email')}>
        <Input type="email" inputMode="email" autoComplete="email" value={email} placeholder="buh@company.ru" onBlur={blur('email')} onChange={(e) => setEmail(e.target.value)} />
      </Field>
      <Field label="Описание услуг" optional>
        <Input value={desc} placeholder="HiEarth — корпоративная подписка на 10 сотрудников" onChange={(e) => setDesc(e.target.value)} />
      </Field>
      {err && <Alert tone="bad" rise>{err}</Alert>}
      <div><Button variant="primary" size="lg" type="submit" block={narrow} loading={busy}>Получить счёт</Button></div>
      <p className="t-xs subtle">Отправляя форму, вы принимаете условия <Link to="/offer" className="link">договора-оферты</Link>.</p>
    </form>
  )

  return (
    <>
      <NarrowHeader back={{ to: '/business', label: 'Для бизнеса' }} right={null} />
      <main id="main" className="container" style={{ maxWidth: 688, paddingTop: 48, paddingBottom: 80 }}>
        <h1 className="t-h1">Счёт на оплату для юрлиц</h1>
        <p className="muted" style={{ marginTop: 6 }}>Сформируем счёт — оплатите его со счёта организации.</p>
        <div className={`collapse ${result ? '' : 'is-open'}`} style={{ marginTop: 28 }} inert={result ? true : undefined}><div>{form}</div></div>
        {result && (
          <section className="card card--elevated card--glow stack gap-3 swap" style={{ marginTop: 28, padding: 28 }} aria-live="polite">
            <Badge tone="ok" style={{ width: 'max-content' }}>Счёт создан</Badge>
            <h2 className="t-h3">Счёт № {result.payment_id} сформирован для {result.company_name || company}</h2>
            <Flap value={Number(result.amount) || amountNum} suffix="₽" size={34} label={`${group(Number(result.amount) || amountNum)} рублей`} />
            {result.confirmation_url && (
              <>
                <p className="t-sm muted">Ссылка — можно скопировать и отправить бухгалтерии:</p>
                <CopyField value={result.confirmation_url} label="Ссылка на оплату счёта" />
              </>
            )}
            <div className={`${narrow ? 'stack' : 'row'} gap-2`}>
              {result.confirmation_url && <Button variant="secondary" size="sm" href={result.confirmation_url} target="_blank" rel="noopener noreferrer" iconRight="external">Перейти к оплате</Button>}
              <Button variant="ghost" size="sm" onClick={again}>Вернуться</Button>
            </div>
          </section>
        )}
      </main>
    </>
  )
}
