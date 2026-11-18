import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { rememberReferral, pendingReferral } from '../lib/referral.js'
import { useAuth } from '../auth/AuthContext.jsx'
import SiteHeader from '../components/SiteHeader.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import Button from '../components/Button.jsx'
import Badge from '../components/Badge.jsx'
import Alert from '../components/Alert.jsx'
import Flap from '../components/Flap.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { kopNum } from '../lib/format.js'
import useSiteInfo, { throughExit } from '../hooks/useSiteInfo.js'

// /r/<code> (spec 06 §6.5, artboard «Приглашение»): the code is saved in the browser (sent with the
// registration, or taken by the «Код друга» field in a young account's cabinet) and the invited friend
// sees what they get. The program's amounts come from /api/v1/referral/terms.
export default function ReferralLinkPage() {
  const { info: site } = useSiteInfo()
  const { code } = useParams()
  const { isLoggedIn } = useAuth()
  const narrow = useMedia('(max-width: 1024px)')
  const [terms, setTerms] = useState(undefined)
  const [saved, setSaved] = useState('')
  usePageMeta('Приглашение в HiEarth', { noindex: true })

  useEffect(() => {
    rememberReferral(code)
    setSaved(pendingReferral())
  }, [code])
  useEffect(() => {
    fetch('/api/v1/referral/terms').then((r) => (r.ok ? r.json() : null)).then(setTerms).catch(() => setTerms(null))
  }, [])

  const shown = String(code || '').trim().toUpperCase()
  const active = terms?.enabled && terms.friend_kop > 0 && saved === shown
  const bonus = active ? kopNum(terms.friend_kop) : 0
  const same = active && terms.referrer_kop === terms.friend_kop

  return (
    <>
      <SiteHeader variant="compact" />
      <main id="main" className="container" style={{ paddingTop: narrow ? 32 : 72, paddingBottom: 80 }}>
        <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : 'minmax(0, 1fr) 440px', gap: narrow ? 32 : 64, alignItems: 'center' }}>
          <div>
            <Hedgehog mood="happy" size={narrow ? 112 : 140} className="pop" />
            <h1 className="t-d1 rise" style={{ marginTop: 20 }}>Друг пригласил вас в&nbsp;HiEarth</h1>
            <p className="t-lead rise" style={{ '--d': '120ms', marginTop: 18, maxWidth: 540 }}>
              Защищённое подключение {throughExit(site.exit_countries)} — для телефона и компьютера.
              {active && (same
                ? ` После вашей первой оплаты вам и другу придёт по ${bonus} ₽ на кошелёк.`
                : ` После вашей первой оплаты вам придёт ${bonus} ₽ на кошелёк.`)}
            </p>
            {isLoggedIn && (
              <Alert tone="info" title="Бонус работает только для новых аккаунтов." className="rise mt-6" style={{ '--d': '160ms', maxWidth: 540 }}>
                Если ваш аккаунт создан меньше недели назад и вы ещё ничего не оплачивали, код можно указать в кабинете.
              </Alert>
            )}
            <div className={`${narrow ? 'stack' : 'row'} gap-3 rise`} style={{ '--d': '220ms', marginTop: 32 }}>
              {isLoggedIn
                ? <Button variant="primary" size="lg" block={narrow} to="/dashboard#referral">В кабинет</Button>
                : <Button variant="primary" size="lg" block={narrow} to="/register">Получить ID бесплатно</Button>}
              <Button variant="ghost" size="lg" block={narrow} to="/#how">Как это работает</Button>
            </div>
          </div>

          <div className="card card--elevated rise" style={{ '--d': '200ms', padding: narrow ? 22 : 28, borderRadius: 28 }}>
            <p className="t-label">Ваш бонус</p>
            <div className="row between gap-3" style={{ marginTop: 10 }}>
              <span className="t-h3">На кошелёк после оплаты</span>
              {active
                ? <Flap value={bonus} suffix="₽" size={36} tone="accent" delayMs={500} label={`${bonus} рублей`} />
                : terms === undefined ? <span className="skel" style={{ width: 96, height: 40 }} aria-hidden="true" /> : <span className="t-h3 subtle">—</span>}
            </div>
            <div className="divider" style={{ margin: '22px 0' }} />
            {active || terms === undefined ? (
              <ul className="plan__list" style={{ margin: 0 }}>
                <li>Код приглашения уже сохранён — ничего вводить не нужно</li>
                <li>Бонус придёт, когда вы оплатите любую подписку деньгами</li>
                <li>Код действует 7 дней после регистрации и до первой оплаты</li>
              </ul>
            ) : (
              <p className="t-sm muted">Код из этой ссылки не действует — бонуса не будет. Зарегистрироваться можно и без него.</p>
            )}
            <div className="copy" style={{ marginTop: 22, paddingRight: 12 }}>
              <span className="copy__v">Код приглашения: {shown}</span>
              {terms !== undefined && (active ? <Badge tone="ok">Применён</Badge> : <Badge tone="warn">Код не действует</Badge>)}
            </div>
          </div>
        </div>
      </main>
    </>
  )
}
