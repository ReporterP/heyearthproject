import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import SiteHeader from '../components/SiteHeader.jsx'
import SiteFooter from '../components/SiteFooter.jsx'
import Button from '../components/Button.jsx'
import Badge from '../components/Badge.jsx'
import Alert from '../components/Alert.jsx'
import Icon from '../components/Icon.jsx'
import Flap from '../components/Flap.jsx'
import ConnectOrb from '../components/ConnectOrb.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import Nomerok from '../components/Nomerok.jsx'
import PlanCard from '../components/PlanCard.jsx'
import Skeleton, { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import useMedia, { PHONE } from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { fetchPlans, splitPlans, savings, featuredId, minPaidPrice, perMonthLabel, termLabel, priceTag } from '../lib/plans.js'
import { dateShort, days, daysLeft, kopNum, traffic } from '../lib/format.js'
import useAccount from '../account/useAccount.js'
import { currentSub, subState } from '../lib/account.js'
import './landing.css'
import useSiteInfo, { exitPlaces, throughExit } from '../hooks/useSiteInfo.js'

// Главная (spec 06 §6.1, artboards «Главная · /» and «Главная · телефон»). Persuade.
// One main action — the free week (or «Получить HiEarth ID» / «В кабинет»), repeated at the end.
const SAMPLE_ID = '7c1e4a9b-2f60-4d8e-a3b5-0c91d27e6f48' // an example tag: the real ID is a UUID (02 §34.3)

const FAQ = [
  { mobile: true, q: 'Работает ли на мобильном интернете?', a: 'Да. Если путь закрыт в вашей сети, приложение переключится на другой. Если сайты не открываются — обновите подписку в Happ: потяните список вниз.' },
  { q: 'Нужны ли мои данные?', a: 'Нет. HiEarth ID выдаётся анонимно. Восстановить его нельзя — сохраните номерок или задайте логин и пароль.' },
  { q: 'Как оплатить и спишете ли вы с карты?', a: 'Вы пополняете кошелёк через ЮKassa картой или СБП. Данные карты мы не храним и ничего не списываем сами.' },
  { q: 'Можно вернуть деньги?', a: <>Да, по условиям возврата — порядок и сроки на странице <Link to="/refund" className="link">«Условия возврата»</Link>.</> },
]

function usePlans() {
  const [state, setState] = useState({ list: [], loading: true, error: false })
  const load = useCallback(() => {
    setState((s) => ({ ...s, loading: true, error: false }))
    fetchPlans().then((list) => setState({ list, loading: false, error: false }), () => setState({ list: [], loading: false, error: true }))
  }, [])
  useEffect(load, [load])
  return { ...state, reload: load }
}

function useReferralTerms() {
  const [t, setT] = useState(null)
  useEffect(() => {
    fetch('/api/v1/referral/terms').then((r) => (r.ok ? r.json() : null)).then(setT).catch(() => setT(null))
  }, [])
  return t && t.enabled && t.friend_kop > 0 ? t : null
}

// «Экран приложения» (06 §6.1 item 7): the person's real subscription when they are signed in and it is
// active; grey «Подключиться» for a guest or without a subscription. The orb is the big button (02 §34.1).
function AppScreen({ phone, isLoggedIn, guestTo }) {
  const { info: site } = useSiteInfo()
  const exit = exitPlaces(site.exit_countries)
  const { data, loading, error } = useAccount(isLoggedIn ? ['subscription', 'traffic'] : [], { optional: ['traffic'] })
  const sub = currentSub(data?.subscription)
  const state = !isLoggedIn || error ? 'none' : loading && !data?.subscription ? 'loading' : subState(sub)
  const live = state === 'active'
  const used = data?.traffic?.periods?.month?.used_bytes
  const orb = live ? { to: '/dashboard', label: 'Готово', aria: 'Подписка активна — открыть кабинет' }
    : state === 'expired' ? { to: '/payment', label: 'Продлить', aria: 'Подписка истекла — продлить' }
    : state === 'loading' ? { to: '/dashboard', label: '', aria: 'Открыть кабинет' }
    : isLoggedIn ? { to: '/payment', label: 'Подключиться', aria: 'Подписки нет — выбрать тариф' }
    : { to: guestTo, label: 'Подключиться', aria: 'Подключиться — получить HiEarth ID' }
  const badge = state === 'loading' ? <Skeleton w={132} h={24} r={999} />
    : live ? <Badge tone="ok">Подписка до {dateShort(sub.valid_until)}</Badge>
    : state === 'expired' ? <Badge tone="warn">Подписка истекла {dateShort(sub.valid_until)}</Badge>
    : <Badge>Нет подписки</Badge>
  return (
    <section className="card card--elevated" style={{ position: 'relative', zIndex: 1, padding: phone ? 22 : 28, borderRadius: phone ? 24 : 28 }} aria-label={isLoggedIn ? 'Ваше подключение' : 'Подключение'}>
      <div className="row between">{phone ? <b aria-hidden="true">HiEarth</b> : <span className="t-h4" aria-hidden="true">HiEarth</span>}{badge}</div>
      <div style={{ display: 'grid', placeItems: 'center', margin: phone ? '22px 0 18px' : '34px 0 30px' }}>
        <ConnectOrb active={live} label={orb.label} to={orb.to} ariaLabel={orb.aria} style={phone ? { width: 168, height: 168 } : undefined} />
      </div>
      <div className="row gap-3" style={{ padding: phone ? '12px 14px' : '14px 16px', borderRadius: 14, background: 'var(--bg)', border: '1px solid var(--line)' }}>
        {exit.nl && <span className={`lp-flag ${live ? '' : 'lp-flag--off'}`} aria-hidden="true" />}
        {phone
          ? <b style={{ flex: 1, color: live ? undefined : 'var(--fg-muted)' }}>{exit.names || 'За рубежом'}</b>
          : (
            <span style={{ flex: 1 }}>
              <b style={{ display: 'block', color: live ? undefined : 'var(--fg-muted)' }}>{exit.names || 'За рубежом'}</b>
              <span className="t-xs subtle">{live ? 'путь выбран автоматически' : 'откроется после подключения'}</span>
            </span>
          )}
        {live && <Flap value={daysLeft(sub.valid_until)} suffix="дн" size={phone ? 15 : 17} delayMs={500} label={`Осталось ${days(daysLeft(sub.valid_until))}`} />}
      </div>
      {!phone && (
        <div className="grid g-2" style={{ marginTop: 10, gap: 10 }}>
          {live
            ? (
              <>
                <div className="lp-well"><p className="t-xs subtle">Получено за месяц</p><p className="t-h4 mono" style={{ marginTop: 4 }}>{used == null ? '—' : traffic(used)}</p></div>
                <div className="lp-well"><p className="t-xs subtle">Тариф</p><p className="t-h4 ok" style={{ marginTop: 4, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{sub.plan_name || '—'}</p></div>
              </>
            )
            : (
              <>
                <div className="lp-well"><p className="t-xs subtle">Получено</p><p className="t-h4 mono subtle" style={{ marginTop: 4 }}>—</p></div>
                <div className="lp-well"><p className="t-xs subtle">Соединение</p><p className="t-h4 subtle" style={{ marginTop: 4 }}>ждёт подключения</p></div>
              </>
            )}
        </div>
      )}
    </section>
  )
}

function PathRow({ tone, title, note, badge, phone }) {
  if (phone) {
    return (
      <div className="row between t-sm" style={{ padding: '10px 12px', borderRadius: 10, background: 'var(--bg)', border: `1px solid ${tone === 'ok' ? 'var(--accent-line)' : 'var(--line)'}` }}>
        <b>{title}</b><Badge tone={tone === 'idle' ? undefined : tone}>{badge}</Badge>
      </div>
    )
  }
  const icon = tone === 'bad' ? <Icon name="x" size={22} strokeWidth={2.4} className="bad" /> : tone === 'ok' ? <Icon name="check" size={22} strokeWidth={2.4} className="ok" /> : <Icon name="circle" size={22} strokeWidth={2.2} className="muted" />
  return (
    <div className="lp-path" style={tone === 'ok' ? { borderColor: 'var(--accent-line)' } : undefined}>
      {icon}
      <span><b>{title}</b> <span className="subtle t-sm">· {note}</span></span>
      <Badge tone={tone === 'idle' ? undefined : tone}>{badge}</Badge>
    </div>
  )
}

export default function LandingPage() {
  const { isLoggedIn } = useAuth()
  const phone = useMedia(PHONE)
  const { info: site } = useSiteInfo()
  const mobile = !!site.mobile_ready // the mobile-network promise only when an entry for them works
  const through = throughExit(site.exit_countries)
  const Through = through.charAt(0).toUpperCase() + through.slice(1)
  const plans = usePlans()
  const ref = useReferralTerms()
  const showSkel = useDelayed(plans.loading, 300)
  usePageMeta('HiEarth — защищённый доступ к интернету')

  const { paid, free } = splitPlans(plans.list)
  const save = savings(plans.list)
  const featured = featuredId(plans.list)
  const minPrice = minPaidPrice(plans.list)
  const allUnlimited = paid.length > 0 && paid.every((p) => !Number(p.traffic_limit_bytes))
  const freeDays = free ? days(Number(free.duration_days) || 7) : ''
  const freeLimit = free ? Number(free.traffic_limit_bytes) || 0 : 0 // the trial is not unlimited: say how much

  const main = isLoggedIn
    ? { label: 'В кабинет', to: '/dashboard' }
    : free ? { label: `Попробовать ${freeDays} бесплатно`, to: '/register' } : { label: 'Получить HiEarth ID', to: '/register' }
  const tariffsLabel = !plans.loading && !plans.error && minPrice ? `Тарифы от ${priceTag(minPrice)}` : 'Тарифы'
  const planTo = (p) => (isLoggedIn ? `/payment?plan=${encodeURIComponent(p.slug)}` : `/register?plan=${encodeURIComponent(p.slug)}`)
  const refAmount = ref ? kopNum(ref.friend_kop) : 0
  const refSame = ref && ref.referrer_kop === ref.friend_kop

  // sticky CTA (phone, guest): shown when the hero button has left the screen and the final CTA is not visible
  const heroCta = useRef(null)
  const finalCta = useRef(null)
  const [heroGone, setHeroGone] = useState(false)
  const [finalSeen, setFinalSeen] = useState(false)
  useEffect(() => {
    if (!phone || isLoggedIn) return undefined
    const io = new IntersectionObserver((es) => {
      for (const e of es) {
        if (e.target === heroCta.current) setHeroGone(!e.isIntersecting && e.boundingClientRect.top < 0)
        if (e.target === finalCta.current) setFinalSeen(e.isIntersecting)
      }
    })
    if (heroCta.current) io.observe(heroCta.current)
    if (finalCta.current) io.observe(finalCta.current)
    return () => io.disconnect()
  }, [phone, isLoggedIn])

  const plansBody = plans.error ? (
    <Alert tone="bad" title="Тарифы недоступны.">
      Не удалось загрузить список. <button type="button" className="link" onClick={plans.reload} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Обновить</button>
    </Alert>
  ) : plans.loading ? (
    <div className="grid g-4" aria-busy="true" style={{ minHeight: 380 }}>
      {showSkel && [0, 1, 2, 3].map((i) => <SkeletonCard key={i} lines={4} big />)}
    </div>
  ) : phone ? (
    <div className="stack gap-3">
      {paid.filter((p) => p.id === featured).map((p) => (
        <div key={p.id} className="plan plan--featured" style={{ padding: 22 }}>
          {save[p.id] > 0 && <span className="plan__ribbon">Выгоднее на {save[p.id]}%</span>}
          <div className="row between gap-3">
            <span><span className="plan__name">{p.name}</span><br /><span className="plan__term">{termLabel(p)}{perMonthLabel(p) ? ` · ${perMonthLabel(p).replace(' в месяц', '/мес')}` : ''}</span></span>
            <span className="plan__price" style={{ margin: 0, fontSize: 34 }}>{priceTag(p.price_rub)}</span>
          </div>
          <Button variant="primary" block to={planTo(p)} style={{ marginTop: 16 }}>Выбрать</Button>
        </div>
      ))}
      {paid.filter((p) => p.id !== featured).map((p) => (
        <Link key={p.id} to={planTo(p)} className="card card--link" style={{ padding: '18px 20px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
          <span><b>{p.name}</b><br /><span className="t-sm subtle">{termLabel(p)}{perMonthLabel(p) ? ` · ${perMonthLabel(p).replace(' в месяц', '/мес')}` : p.direct_routing_enabled ? ' · РФ-сервисы' : ''}</span></span>
          <b className="t-h3">{priceTag(p.price_rub)}</b>
        </Link>
      ))}
    </div>
  ) : (
    <div
      className={`grid ${paid.length >= 4 ? 'g-4' : paid.length === 3 ? 'g-3' : 'g-2'}`}
      style={paid.length <= 2 ? { maxWidth: 760, margin: '0 auto' } : undefined}
    >
      {paid.map((p) => (
        <PlanCard key={p.id} plan={p} featured={p.id === featured} saving={save[p.id]} to={planTo(p)} />
      ))}
    </div>
  )

  return (
    <>
      <SiteHeader transparentTop />
      <main id="main" className="lp-page">
        {/* 1 · hero */}
        <section className="lp-hero">
          <div className="container lp-hero__grid">
            <div>
              <h1 className={`${phone ? '' : 't-d1'} rise`} style={phone ? { fontSize: 40, lineHeight: 1.04, letterSpacing: '-.045em', fontWeight: 800 } : undefined}>
                Защищённый доступ к&nbsp;интернету в&nbsp;одну кнопку
              </h1>
              <p className={`${phone ? 'muted' : 't-lead'} rise`} style={{ '--d': '120ms', marginTop: phone ? 16 : 24, maxWidth: 540, fontSize: phone ? 16 : undefined, lineHeight: phone ? 1.55 : undefined }}>
                {phone
                  ? `${Through}${mobile ? ' — по Wi-Fi и на мобильном интернете' : ''}. Без почты, телефона и привязки карты.`
                  : `Сайты и приложения открываются ${through}${mobile ? ' — по Wi-Fi и на мобильном интернете' : ''}. Без почты, без телефона, без привязки карты.`}
              </p>
              {phone && <div className="rise" style={{ '--d': '200ms', marginTop: 26 }}><AppScreen phone isLoggedIn={isLoggedIn} guestTo={main.to} /></div>}
              <div ref={heroCta} className={`${phone ? 'stack gap-2' : 'row wrap gap-3'} rise`} style={{ '--d': phone ? '280ms' : '200ms', marginTop: phone ? 20 : 36 }}>
                <Button variant={isLoggedIn ? 'secondary' : 'primary'} size="lg" block={phone} to={main.to} iconRight={phone || isLoggedIn ? undefined : 'arrowRight'}>{main.label}</Button>
                <Button variant="secondary" size="lg" block={phone} to="/#plans">{tariffsLabel}</Button>
              </div>
              {!phone && <p className="t-sm subtle rise" style={{ '--d': '280ms', marginTop: 28 }}>iPhone · Android · Windows · macOS — через бесплатное приложение Happ</p>}
            </div>
            {!phone && (
              <div className="rise" style={{ '--d': '200ms', position: 'relative', justifySelf: 'end', width: '100%', maxWidth: 460, marginTop: 40 }}>
                <Hedgehog mood="idle" size={112} style={{ position: 'absolute', right: 28, top: -66, zIndex: 0 }} />
                <AppScreen isLoggedIn={isLoggedIn} guestTo={main.to} />
              </div>
            )}
          </div>
        </section>

        {/* 2 · bento (id how) */}
        <section id="how" className="lp-section">
          <div className="container">
            <h2 className={phone ? 'lp-h2' : 't-d2'} style={{ maxWidth: 720 }}>{phone ? 'Всё, что нужно' : <>Всё, что нужно, — и&nbsp;ничего лишнего</>}</h2>
            <div className="lp-bento" style={{ marginTop: phone ? 22 : 48 }}>
              {mobile && (
              <article className="card" style={{ gridColumn: 'span 4', padding: phone ? 20 : 30 }}>
                <h3 className={phone ? 't-h3' : 't-h2'}>{phone ? 'Работает на мобильном' : 'Работает на мобильном интернете'}</h3>
                <p className={`muted ${phone ? 't-sm' : ''}`} style={{ marginTop: phone ? 6 : 10, maxWidth: 520 }}>
                  {phone ? 'Оператор закрыл путь — приложение переходит на соседний.' : 'В подписке несколько способов подключения. Если оператор закрыл один, приложение само переходит на соседний — вы ничего не делаете.'}
                </p>
                <div className="stack gap-2" style={{ marginTop: phone ? 14 : 24 }} role="img" aria-label="Схема: основной путь закрыт, вход для мобильных сетей работает, запасной канал готов">
                  <PathRow phone={phone} tone="bad" title="Основной путь" note="закрыт в вашей сети" badge="Закрыт" />
                  <PathRow phone={phone} tone="ok" title={phone ? 'Вход для мобильных' : 'Вход для мобильных сетей'} note="вы здесь" badge="Работает" />
                  {!phone && <PathRow tone="idle" title="Запасной канал" note="наготове" badge="Готов" />}
                </div>
              </article>
              )}
              <article className={phone ? 'card row gap-4' : 'card'} style={{ gridColumn: mobile ? 'span 2' : 'span 6', padding: phone ? 20 : 30, display: 'flex', flexDirection: phone ? 'row' : 'column', alignItems: phone ? 'flex-start' : undefined }}>
                <div style={phone ? { flex: 1 } : undefined}>
                  <h3 className={phone ? 't-h3' : 't-h2'}>Анонимно</h3>
                  <p className={`muted ${phone ? 't-sm' : ''}`} style={{ marginTop: phone ? 6 : 10 }}>{phone ? 'Вместо почты и телефона — номерок, HiEarth ID.' : 'Вместо почты и телефона — номерок: HiEarth ID за один клик.'}</p>
                </div>
                {phone ? (
                  <div className="tag tag--idle" aria-hidden="true" style={{ padding: '40px 12px 12px', borderRadius: '32px 32px 14px 14px', marginTop: 18 }}>
                    <span className="tag__hook" style={{ top: -24, height: 36 }} />
                    <span className="tag__id" style={{ fontSize: 14 }}>{SAMPLE_ID.slice(0, 4)}<br />{SAMPLE_ID.slice(-4)}</span>
                  </div>
                ) : (
                  <div style={{ marginTop: 'auto', paddingTop: 40, display: 'flex', justifyContent: 'center' }} aria-hidden="true">
                    <Nomerok id={SAMPLE_ID} variant="idle" label="номерок" railWidth="100%" />
                  </div>
                )}
              </article>
              <article className="card" style={{ gridColumn: 'span 3', padding: phone ? 20 : 30 }}>
                <h3 className={phone ? 't-h3' : 't-h2'}>Честная оплата</h3>
                <p className={`muted ${phone ? 't-sm' : ''}`} style={{ marginTop: phone ? 6 : 10, maxWidth: 440 }}>
                  {phone ? 'Подписка платится с кошелька и продлевается, только если денег хватает. Карту не привязываем.' : 'Пополняете кошелёк картой или через СБП — подписка оплачивается с баланса и продлевается, только если денег хватает. Карту не привязываем.'}
                </p>
                {!phone && (
                  <div className="row gap-4" style={{ marginTop: 26, padding: '18px 20px', borderRadius: 16, background: 'var(--bg)', border: '1px solid var(--line)' }} aria-hidden="true">
                    <span className="t-sm muted" style={{ flex: 1 }}>На кошельке</span>
                    <Flap value={430} suffix="₽" size={30} label="" />
                  </div>
                )}
              </article>
              {!phone && (
                <article className="card" style={{ gridColumn: 'span 3', padding: 30 }}>
                  <h3 className="t-h2">Все ваши устройства</h3>
                  <p className="muted" style={{ marginTop: 10 }}>Один аккаунт — телефон, планшет и компьютер. Подключение в приложении Happ, оно бесплатное.</p>
                  <div className="grid g-2" style={{ marginTop: 22, gap: 10 }}>
                    {['iPhone и iPad', 'Android', 'Windows', 'macOS'].map((d) => <span key={d} className="btn btn--secondary" style={{ justifyContent: 'flex-start', cursor: 'default', pointerEvents: 'none' }}>{d}</span>)}
                  </div>
                </article>
              )}
            </div>

            {/* 3 · three steps */}
            {phone ? (
              <>
                <h2 className="lp-h2" style={{ marginTop: 48 }}>Три шага</h2>
                <ol className="stack gap-4" style={{ marginTop: 20 }}>
                  {[['Получите HiEarth ID', 'Один клик, без почты и телефона.'], ['Установите Happ', 'Бесплатно для iPhone, Android, Windows, macOS.'], ['Нажмите «Добавить в Happ»', 'Подписка добавится сама — включайте.']].map(([t, d], i) => (
                    <li key={t} className="row gap-3" style={{ alignItems: 'flex-start' }}>
                      <span className="kbd" style={{ height: 30, minWidth: 30, justifyContent: 'center' }}>{i + 1}</span>
                      <span><b>{t}</b><br /><span className="muted t-sm">{d}</span></span>
                    </li>
                  ))}
                </ol>
              </>
            ) : (
              <ol className="grid g-3" style={{ marginTop: 96, gap: 32 }}>
                {[['Получите HiEarth ID', 'Один клик, без почты и телефона. Сохраните номерок — восстановить его нельзя.'], ['Установите Happ', 'Бесплатное приложение для iPhone, Android, Windows и macOS.'], ['Нажмите «Добавить в Happ»', 'Подписка добавится сама. Включите — и всё, дальше путь выбирается автоматически.']].map(([t, d], i) => (
                  <li key={t} className="lp-step"><span className="lp-step__n" aria-hidden="true">{i + 1}</span><h3 className="t-h3">{t}</h3><p className="muted t-sm" style={{ marginTop: 8 }}>{d}</p></li>
                ))}
              </ol>
            )}
          </div>
        </section>

        {/* 4 · plans */}
        <section id="plans" className="lp-section">
          <div className="container">
            <div className="row wrap between" style={{ alignItems: 'flex-end', gap: phone ? 8 : 20 }}>
              <h2 className={phone ? 'lp-h2' : 't-d2'}>Тарифы</h2>
              {(allUnlimited || free) && (
                <p className={`muted ${phone ? 't-sm' : ''}`} style={{ maxWidth: 440 }}>
                  {phone
                    ? [allUnlimited && 'Безлимитный трафик в платных тарифах.', free && `Неделя — бесплатно${freeLimit ? `, ${traffic(freeLimit)}` : ''}.`].filter(Boolean).join(' ')
                    : [allUnlimited && 'Безлимитный трафик во всех платных тарифах.', free && `Первая неделя — бесплатно и без карты${freeLimit ? `, ${traffic(freeLimit)} трафика` : ''}.`].filter(Boolean).join(' ')}
                </p>
              )}
            </div>
            <div style={{ marginTop: phone ? 22 : 48 }}>{plansBody}</div>

            {ref && (
              <div id="referral" className={`card ${phone ? 'stack gap-4' : 'row gap-6'}`} style={{ marginTop: 24, padding: phone ? 20 : '20px 28px' }}>
                {!phone && <Hedgehog mood="happy" size={84} style={{ flex: 'none' }} />}
                <p style={{ flex: 1 }}>
                  <b>{refSame ? `Приведите друга — по ${refAmount} ₽ каждому.` : `Приведите друга — вам ${kopNum(ref.referrer_kop)} ₽, другу ${refAmount} ₽.`}</b>{' '}
                  <span className="muted">Ссылка в кабинете: когда друг оплатит подписку, деньги придут на оба кошелька.</span>
                </p>
                <Button variant="secondary" block={phone} to={isLoggedIn ? '/dashboard#referral' : '/register'}>Получить ссылку</Button>
              </div>
            )}
          </div>
        </section>

        {/* 5 · questions */}
        <section id="faq" className="lp-section">
          <div className="container lp-faq">
            <div>
              <h2 className={phone ? 'lp-h2' : 't-d2'}>Вопросы</h2>
              <p className="muted" style={{ marginTop: phone ? 8 : 16 }}>Не нашли ответ? <Link to="/help" className="link">Вся помощь</Link></p>
            </div>
            <div className="acc">
              {FAQ.filter((f) => mobile || !f.mobile).map((f) => (
                <details key={f.q}>
                  <summary style={phone ? { fontSize: 16 } : undefined}>{f.q}</summary>
                  <p className={`acc__b ${phone ? 't-sm' : ''}`}>{f.a}</p>
                </details>
              ))}
            </div>
          </div>
        </section>

        {/* 6 · final CTA */}
        <section style={{ padding: phone ? '8px 0 40px' : '0 0 96px' }}>
          <div className="container">
            <div ref={finalCta} className={`card card--glow ${phone ? 'stack gap-4' : 'row wrap between'}`} style={phone ? { padding: 26, borderRadius: 24, alignItems: 'center', textAlign: 'center' } : { padding: '48px 56px', borderRadius: 28, gap: 32 }}>
              <div className="row gap-8">
                {!phone && <ConnectOrb active small to={isLoggedIn ? '/dashboard' : '/register'} ariaLabel={isLoggedIn ? 'Открыть кабинет' : 'Подключиться — получить HiEarth ID'} />}
                <div>
                  <h2 className={phone ? 't-h2' : 't-h1'}>Подключитесь за минуту</h2>
                  {free && !phone && <p className="muted" style={{ marginTop: 8 }}>Неделя бесплатно. Карта не нужна.</p>}
                </div>
              </div>
              {isLoggedIn
                ? <Button variant="secondary" size="lg" block={phone} to="/dashboard">В кабинет</Button>
                : <Button variant="primary" size="lg" block={phone} to="/register">Получить ID бесплатно</Button>}
            </div>
          </div>
        </section>
      </main>
      <SiteFooter compact={phone} />

      {phone && !isLoggedIn && (
        <div className={`sticky-cta ${heroGone && !finalSeen ? 'is-shown' : ''}`} aria-hidden={!(heroGone && !finalSeen)}>
          <Button variant="primary" size="lg" block to="/register" tabIndex={heroGone && !finalSeen ? 0 : -1}>{free ? `Попробовать ${freeDays} бесплатно` : 'Получить HiEarth ID'}</Button>
        </div>
      )}
    </>
  )
}
