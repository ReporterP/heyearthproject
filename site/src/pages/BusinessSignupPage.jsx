import { useRef, useState } from 'react'
import { useAuth } from '../auth/AuthContext.jsx'
import { createTenant } from '../api/tenant.js'
import NarrowHeader from '../components/NarrowHeader.jsx'
import Steps from '../components/Steps.jsx'
import Alert from '../components/Alert.jsx'
import Button from '../components/Button.jsx'
import CopyField from '../components/CopyField.jsx'
import Field, { Input } from '../components/Field.jsx'
import Flap from '../components/Flap.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import { Segmented } from '../components/Controls.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { B2B, estimate } from '../lib/b2b.js'
import { group } from '../lib/format.js'

// Подключение компании /business/signup (spec 10 §10.2): Параметры → Контакт → Готово, with the
// estimate on the right flipping as the parameters change. POST /api/public/create-tenant.
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export default function BusinessSignupPage() {
  const { isLoggedIn } = useAuth()
  const narrow = useMedia('(max-width: 1024px)')
  usePageMeta('Подключение бизнеса — HiEarth')
  const [step, setStep] = useState(0)
  const [users, setUsers] = useState('10')
  const [servers, setServers] = useState('ours') // ours | own
  const [own, setOwn] = useState('5')
  const [company, setCompany] = useState('')
  const [email, setEmail] = useState('')
  const [tried, setTried] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [result, setResult] = useState(null)
  const inFlight = useRef(false)

  const n = Math.max(1, parseInt(users, 10) || 0)
  const ownN = servers === 'own' ? Math.max(0, parseInt(own, 10) || 0) : 0
  const total = estimate({ users: n, useOur: servers === 'ours', own: ownN })
  const errs = { company: !company.trim() ? 'Укажите название компании' : null, email: !email.trim() ? 'Укажите email' : !EMAIL_RE.test(email.trim()) ? 'Проверьте адрес почты — в нём должна быть «@» и домен' : null }

  async function submit(e) {
    e.preventDefault()
    setTried(true)
    if (errs.company || errs.email || inFlight.current) return
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      const r = await createTenant({ email: email.trim(), companyName: company.trim(), specs: { users: n, ownServers: ownN || B2B.includedServers, useProviderServers: servers === 'ours' } })
      setResult(r || {})
      setStep(2)
    } catch (e2) {
      setErr(e2.message && /[а-я]/i.test(e2.message) ? e2.message : 'Не удалось создать аккаунт компании — попробуйте ещё раз.')
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  const steps = <Steps label="Шаги подключения" items={[{ label: 'Параметры', onClick: step === 1 ? () => setStep(0) : undefined }, { label: 'Контакт' }, { label: 'Готово' }]} current={step} />

  if (step === 2) {
    const claim = result?.claim_url || ''
    const claimPath = claim ? new URL(claim, window.location.origin).pathname : ''
    return (
      <>
        <NarrowHeader back={{ to: '/business', label: 'Для бизнеса' }} center={steps} right={null} />
        <main id="main" className="container" style={{ maxWidth: 1040, paddingTop: 48, paddingBottom: 80 }}>
          <section className="card swap" style={{ display: 'grid', gridTemplateColumns: narrow ? '1fr' : '110px minmax(0,1fr)', gap: 24, alignItems: 'center', padding: 28 }}>
            <Hedgehog mood="happy" size={110} />
            <div className="stack gap-3">
              <h1 className="t-h2">Аккаунт компании создан</h1>
              <p className="muted t-sm">
                {claim
                  ? 'Зарегистрируйтесь на сайте, затем откройте эту ссылку — мы привяжем ваш аккаунт к компании.'
                  : `Письмо с инструкциями отправлено на ${email.trim()}. Войдите в свой аккаунт — компания уже привязана к нему.`}
              </p>
              {claim && <CopyField value={claim} label="Ссылка для привязки" />}
              <div className={`${narrow ? 'stack' : 'row'} gap-2`}>
                {isLoggedIn
                  ? <Button variant="primary" to={claimPath || '/business/dashboard'}>{claimPath ? 'Привязать мой аккаунт' : 'Кабинет компании'}</Button>
                  : (
                    <>
                      <Button variant="primary" to={claimPath ? `/register?redirect=${encodeURIComponent(claimPath)}` : '/register'}>Зарегистрироваться</Button>
                      <Button variant="ghost" to={claimPath ? `/login?redirect=${encodeURIComponent(claimPath)}` : '/login'}>Войти в аккаунт</Button>
                    </>
                  )}
              </div>
            </div>
          </section>
        </main>
      </>
    )
  }

  return (
    <>
      <NarrowHeader back={{ to: '/business', label: 'Для бизнеса' }} center={steps} right={null} />
      <main id="main" className="container" style={{ maxWidth: 1040, paddingTop: 48, paddingBottom: 80 }}>
        <h1 className="t-h1">Подключение бизнес-аккаунта</h1>
        <p className="muted" style={{ marginTop: 6 }}>Без предоплаты на сайте. Оплата — после входа в панель управления.</p>
        <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : 'minmax(0, 1fr) 360px', gap: 24, marginTop: 28, alignItems: 'start' }}>
          {step === 0 ? (
            <form className="card stack gap-4 swap" style={{ padding: 28 }} onSubmit={(e) => { e.preventDefault(); setStep(1) }} noValidate>
              <Field label="Сколько сотрудников" hint={`${B2B.perUser} ₽ в месяц за каждого`}>
                <Input mono inputMode="numeric" value={users} onChange={(e) => setUsers(e.target.value.replace(/\D/g, '').slice(0, 5))} onBlur={() => setUsers(String(n))} />
              </Field>
              <div className="field">
                <span className="field__label" id="srv-l">Серверы</span>
                <Segmented label="Серверы" value={servers} onChange={setServers} options={[{ value: 'ours', label: 'Наши' }, { value: 'own', label: 'Свои' }]} />
              </div>
              {servers === 'own' && (
                <Field label="Сколько своих серверов" hint={`До ${B2B.includedServers} включены, каждый следующий — +${B2B.perExtraServer} ₽ в месяц`} className="swap">
                  <Input mono inputMode="numeric" value={own} onChange={(e) => setOwn(e.target.value.replace(/\D/g, '').slice(0, 4))} />
                </Field>
              )}
              <div><Button variant="primary" type="submit" iconRight="arrowRight">Дальше</Button></div>
            </form>
          ) : (
            <form className="card stack gap-4 swap" style={{ padding: 28 }} onSubmit={submit} noValidate>
              <Field label="Название компании" error={tried && errs.company}>
                <Input value={company} placeholder="ООО «Компания»" autoComplete="organization" onChange={(e) => setCompany(e.target.value)} />
              </Field>
              <Field label="Email" error={tried && errs.email}>
                <Input type="email" inputMode="email" autoComplete="email" value={email} placeholder="you@company.ru" onChange={(e) => setEmail(e.target.value)} />
              </Field>
              {err && <Alert tone="bad" rise>{err}</Alert>}
              <div className="row gap-2" style={{ marginTop: 6 }}>
                <Button variant="ghost" onClick={() => setStep(0)} disabled={busy}>Назад</Button>
                <Button variant="primary" type="submit" loading={busy}>Получить доступ</Button>
              </div>
            </form>
          )}
          <aside className="card card--elevated" aria-live="polite">
            <span className="t-label">Предварительная оценка</span>
            <div style={{ marginTop: 10 }}><Flap value={total} suffix="₽/мес" size={40} label={`${group(total)} рублей в месяц`} /></div>
            <div className="stack gap-1 t-sm muted" style={{ marginTop: 14 }}>
              <span>{n} {n % 10 === 1 && n % 100 !== 11 ? 'сотрудник' : n % 10 >= 2 && n % 10 <= 4 && (n % 100 < 12 || n % 100 > 14) ? 'сотрудника' : 'сотрудников'} × {B2B.perUser} ₽</span>
              {servers === 'ours' && <span>Наши серверы — {group(B2B.ourServers)} ₽</span>}
              {servers === 'own' && ownN > B2B.includedServers && <span>Свои серверы сверх {B2B.includedServers}: {ownN - B2B.includedServers} × {B2B.perExtraServer} ₽</span>}
            </div>
            <hr className="divider" style={{ margin: '16px 0' }} />
            <p className="t-sm muted">Оплата — через пополнение кошелька в панели. Сейчас ничего не спишется.</p>
          </aside>
        </div>
      </main>
    </>
  )
}
