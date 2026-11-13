import { useState, useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import Fireflies from '../components/Fireflies.jsx'
import useReveal from '../hooks/useReveal.js'

/* ── Icons (SVG stroke, Lucide-style, single weight) ── */
const Arrow = ({ className = 'w-4 h-4' }) => (
  <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M13 6l6 6-6 6" /></svg>
)
const Check = ({ className = 'w-4 h-4' }) => (
  <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.4} strokeLinecap="round" strokeLinejoin="round"><path d="M5 12l5 5L20 6" /></svg>
)
const Tree = ({ className = 'w-6 h-6' }) => (
  <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round"><path d="M9 22V4M15 22V6M6 9c0-2 2-3 3-3M18 11c0-2-2-4-3-4" /></svg>
)
const IconLeaf = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round"><path d="M11 20A7 7 0 019.8 6.1C15.5 5 17 4.48 19 2c1 2 2 4.18 2 8 0 5.5-4.78 10-10 10z" /><path d="M2 21c0-3 1.85-5.36 5.08-6" /></svg>)
const IconDevices = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="14" height="11" rx="2" /><path d="M7 19h6M10 15v4" /><rect x="16" y="9" width="6" height="11" rx="1.5" /></svg>)
const IconShield = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round"><path d="M12 3l7 3v5c0 4.5-3 8.5-7 10-4-1.5-7-5.5-7-10V6z" /><path d="M9 12l2 2 4-4" /></svg>)
const IconBolt = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round"><path d="M13 2L4 14h7l-2 8 9-12h-7z" /></svg>)

const features = [
  { Icon: IconLeaf, title: 'Эко-маршрутизация', desc: 'Оптимизированные маршруты снижают энергопотребление и ускоряют доступ к ресурсам.' },
  { Icon: IconDevices, title: 'Все устройства', desc: 'Android, Windows, iOS, macOS, Linux. Один аккаунт — все платформы сразу.' },
  { Icon: IconShield, title: 'Приватность', desc: 'Соединение неотличимо от обычного защищённого HTTPS. Данные под шифрованием на всём пути.' },
  { Icon: IconBolt, title: 'Высокая скорость', desc: 'Минимальная задержка благодаря умной сети маршрутизации и близким шлюзам.' },
]

const steps = [
  { num: '01', title: 'Регистрация', desc: 'Получите уникальный HiEarth ID за один клик. Без email, без номера телефона.' },
  { num: '02', title: 'Подключение', desc: 'Установите приложение и настройте маршрутизацию на любом устройстве за пару кликов.' },
  { num: '03', title: 'Готово', desc: 'Пользуйтесь быстрым, приватным и стабильным доступом к интернету.' },
]

const reviews = [
  { name: 'Мария К.', role: 'дизайнер', text: 'Просто работает. Поставила приложение, ввела ID — и всё летает без единого сбоя уже полгода.' },
  { name: 'Дмитрий А.', role: 'разработчик', text: 'Скорость честно радует, пинг до зарубежных сервисов ниже, чем я ожидал. Настройка — минута.' },
  { name: 'Иван С.', role: 'предприниматель', text: 'Перевёл на HiEarth всю команду через бизнес-кабинет. Счета, роли, оплата — всё прозрачно.' },
]

const faqs = [
  { q: 'Нужны ли мои данные для регистрации?', a: 'Нет. HiEarth ID выдаётся анонимно за один клик — без почты, телефона и паспорта. Сохраните ID, он нужен для входа.' },
  { q: 'На каких устройствах работает?', a: 'Android, iOS, Windows, macOS и Linux. Один аккаунт подключается сразу к нескольким устройствам, в зависимости от тарифа.' },
  { q: 'Как оплатить?', a: 'Картой или через СБП. Оплата проходит через защищённую систему ЮKassa, данные карты мы не храним. Есть автопродление — его можно отключить в любой момент.' },
  { q: 'Можно вернуть деньги?', a: 'Да, по условиям возврата. Неиспользованный период подписки возвращается — подробности на странице «Условия возврата».' },
  { q: 'Это стабильно работает?', a: 'Да. Инфраструктура сама подбирает лучший маршрут и переключается при сбоях, поэтому соединение остаётся ровным без капч и обрывов.' },
]

function formatTraffic(bytes) {
  if (!bytes) return 'Безлимит'
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(0)} ГБ` : `${(bytes / (1024 * 1024)).toFixed(0)} МБ`
}
function formatDuration(days) {
  if (days >= 365) { const y = Math.round(days / 365); return `${y} ${y === 1 ? 'год' : y < 5 ? 'года' : 'лет'}` }
  if (days >= 30) return `${Math.round(days / 30)} мес.`
  return `${days} дн.`
}

/* Count-up metric, animates 0→target on first reveal, tabular to avoid jitter */
function CountStat({ prefix = '', target, decimals = 0, suffix = '', label }) {
  const ref = useRef(null)
  const [val, setVal] = useState(0)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reduce || typeof IntersectionObserver === 'undefined') { setVal(target); return }
    const io = new IntersectionObserver((entries) => {
      entries.forEach((e) => {
        if (!e.isIntersecting) return
        io.unobserve(e.target)
        const dur = 900, t0 = performance.now()
        const step = (t) => {
          const p = Math.min(1, (t - t0) / dur)
          const eased = 1 - Math.pow(1 - p, 3)
          setVal(target * eased)
          if (p < 1) requestAnimationFrame(step)
        }
        requestAnimationFrame(step)
      })
    }, { threshold: 0.4 })
    io.observe(el)
    return () => io.disconnect()
  }, [target])
  return (
    <div ref={ref} className="text-center">
      <p className="serif text-3xl md:text-4xl font-semibold tracking-tight tabular-nums text-text-primary">
        {prefix}{val.toFixed(decimals)}{suffix}
      </p>
      <p className="text-xs text-text-secondary mt-1.5 font-medium">{label}</p>
    </div>
  )
}

export default function LandingPage() {
  const { isLoggedIn } = useAuth()
  const [plans, setPlans] = useState([])
  const [plansLoading, setPlansLoading] = useState(true)
  const [openFaq, setOpenFaq] = useState(0)
  const [refTerms, setRefTerms] = useState(null)

  useEffect(() => {
    fetch('/api/v1/referral/terms').then(r => r.ok ? r.json() : null).then(setRefTerms).catch(() => {})
  }, [])

  useEffect(() => {
    fetch('/api/v1/plans')
      .then(r => r.json())
      .then(d => setPlans(Array.isArray(d) ? d.filter(p => p.price_rub > 0) : []))
      .catch(() => {})
      .finally(() => setPlansLoading(false))
  }, [])

  useReveal([plans, plansLoading])

  return (
    <>
      {/* ─── Hero ─── */}
      <section className="relative overflow-hidden pt-24 pb-28 md:pt-32 md:pb-36">
        <Fireflies density={13000} />
        {/* drift blobs */}
        <div className="pointer-events-none absolute inset-0 overflow-hidden">
          <div className="blob-a absolute top-16 left-[8%] w-72 h-72 rounded-full bg-brand/14 blur-[110px]" />
          <div className="blob-b absolute bottom-6 right-[8%] w-80 h-80 rounded-full bg-accent/10 blur-[130px]" />
        </div>

        <div className="relative max-w-4xl mx-auto px-6 text-center">
          <div className="eyebrow mb-7 animate-fade-in">
            <span className="w-1.5 h-1.5 rounded-full bg-brand pulse-dot" />
            Устойчивый интернет нового поколения
          </div>

          <h1 className="serif text-4xl sm:text-5xl md:text-[4.1rem] font-semibold leading-[1.08] animate-fade-in-up" style={{ lineHeight: 1.1 }}>
            Тихий интернет,<br className="hidden sm:block" /> в котором всё <span className="italic text-brand">просто работает</span>
          </h1>

          <p className="mt-7 text-base md:text-lg text-text-secondary max-w-xl mx-auto leading-relaxed animate-fade-in-up delay-200">
            HiEarth — устойчивый доступ с умной эко-маршрутизацией. Быстро, приватно и без лишнего шума на всех ваших устройствах.
          </p>

          <div className="mt-9 flex flex-wrap justify-center gap-3.5 animate-fade-in-up delay-300">
            <Link to={isLoggedIn ? '/dashboard' : '/register'} className="btn-primary">
              {isLoggedIn ? 'В кабинет' : 'Начать бесплатно'} <Arrow />
            </Link>
            <a href="#plans" className="btn-ghost">Смотреть тарифы</a>
          </div>

          <div className="mt-16 grid grid-cols-3 max-w-md mx-auto divide-x divide-brand/12 animate-fade-in-up delay-500">
            <CountStat target={99.9} decimals={1} suffix="%" label="Аптайм" />
            <CountStat prefix="<" target={15} suffix=" мс" label="Задержка" />
            <CountStat target={50} suffix="+" label="Локаций" />
          </div>
        </div>
      </section>

      {/* ─── Steps ─── */}
      <section className="relative py-20 md:py-28">
        <div className="max-w-5xl mx-auto px-6">
          <div className="text-center mb-14 reveal">
            <p className="eyebrow mb-4">Начало работы</p>
            <h2 className="text-3xl md:text-4xl font-semibold">Три шага до тишины</h2>
          </div>
          <div className="grid md:grid-cols-3 gap-6">
            {steps.map((s, i) => (
              <div key={s.num} className="card card-hover p-7 reveal" style={{ transitionDelay: `${Math.min(i * 90, 220)}ms` }}>
                <span className="serif text-2xl font-semibold text-brand tabular-nums">{s.num}</span>
                <h3 className="text-lg font-semibold mt-4 mb-2">{s.title}</h3>
                <p className="text-sm text-text-secondary leading-relaxed">{s.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ─── Features ─── */}
      <section className="relative py-20 md:py-28">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/12 to-transparent" />
        <div className="max-w-6xl mx-auto px-6">
          <div className="text-center mb-14 reveal">
            <p className="eyebrow mb-4">Преимущества</p>
            <h2 className="text-3xl md:text-4xl font-semibold">Почему HiEarth</h2>
          </div>
          <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-5">
            {features.map((f, i) => (
              <div key={f.title} className="group card card-hover p-6 reveal" style={{ transitionDelay: `${Math.min(i * 70, 220)}ms` }}>
                <div className="w-12 h-12 rounded-xl bg-brand/12 border border-brand/20 text-brand flex items-center justify-center mb-4 transition-transform duration-300 group-hover:scale-110">
                  <f.Icon className="w-6 h-6" />
                </div>
                <h3 className="text-base font-semibold mb-2">{f.title}</h3>
                <p className="text-sm text-text-secondary leading-relaxed">{f.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ─── Plans ─── */}
      <section id="plans" className="relative py-20 md:py-28 overflow-hidden">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/12 to-transparent" />
        <div className="pointer-events-none absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[560px] h-[380px] bg-brand/6 rounded-full blur-[130px]" />
        <div className="relative max-w-5xl mx-auto px-6">
          <div className="text-center mb-14 reveal">
            <p className="eyebrow mb-4">Тарифы</p>
            <h2 className="text-3xl md:text-4xl font-semibold">Прозрачные цены</h2>
            <p className="mt-4 text-text-secondary max-w-lg mx-auto">Без скрытых платежей. Оплата помесячно, отмена в любой момент.</p>
          </div>

          {plansLoading ? (
            <div className="grid gap-6 md:grid-cols-3">
              {[0, 1, 2].map(i => <div key={i} className="card h-80 animate-pulse-glow" />)}
            </div>
          ) : plans.length === 0 ? (
            <div className="card p-10 text-center max-w-md mx-auto">
              <p className="text-text-secondary">Тарифы временно недоступны. Загляните чуть позже или напишите нам.</p>
            </div>
          ) : (
            <div className={`grid gap-6 ${plans.length >= 3 ? 'md:grid-cols-3' : 'md:grid-cols-2 max-w-3xl mx-auto'}`}>
              {plans.map((p, i) => {
                const highlighted = i === Math.min(Math.floor(plans.length / 2), plans.length - 1)
                const pf = typeof p.features === 'string' ? JSON.parse(p.features || '{}') : (p.features || {})
                const feats = [p.description, `Трафик: ${formatTraffic(p.traffic_limit_bytes)}`, `Срок: ${formatDuration(p.duration_days)}`]
                if (pf.traffic_masking) feats.push('Приватная маршрутизация')
                if (pf.priority_support) feats.push('Приоритетная поддержка')
                if (p.direct_routing_enabled) feats.push('Доступ к РФ-сервисам')
                return (
                  <div key={p.id} className={`group relative p-8 rounded-[22px] reveal card-hover ${highlighted ? 'card-feat' : 'card'}`} style={{ transitionDelay: `${Math.min(i * 90, 220)}ms` }}>
                    {highlighted && (
                      <span className="absolute -top-3 left-1/2 -translate-x-1/2 px-3.5 py-1 rounded-full bg-accent text-[#2a1e02] text-[11px] font-bold uppercase tracking-wide">Популярный</span>
                    )}
                    <div className="flex items-center justify-between mb-4">
                      <h3 className="serif text-xl font-semibold">{p.name}</h3>
                      <span className="tree-sway text-brand"><Tree className="w-7 h-7" /></span>
                    </div>
                    <div className="flex items-baseline gap-1.5 mb-6">
                      <span className="serif text-5xl font-semibold tracking-tight tabular-nums">{p.price_rub}</span>
                      <span className="text-text-secondary text-sm">₽ / {formatDuration(p.duration_days)}</span>
                    </div>
                    <ul className="space-y-2.5 mb-8">
                      {feats.map(f => (
                        <li key={f} className="flex items-start gap-2.5 text-sm text-text-secondary">
                          <Check className="w-4 h-4 mt-0.5 text-brand flex-shrink-0" /><span>{f}</span>
                        </li>
                      ))}
                    </ul>
                    <Link to="/payment" className={highlighted ? 'btn-primary w-full' : 'btn-ghost w-full'}>Выбрать</Link>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </section>

      {/* ─── Reviews ─── */}
      <section className="relative py-20 md:py-28">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/12 to-transparent" />
        <div className="max-w-5xl mx-auto px-6">
          <div className="text-center mb-14 reveal">
            <p className="eyebrow mb-4">Отзывы</p>
            <h2 className="text-3xl md:text-4xl font-semibold">Тихо, но за нас говорят</h2>
          </div>
          <div className="grid md:grid-cols-3 gap-5">
            {reviews.map((r, i) => (
              <figure key={r.name} className="card p-6 reveal" style={{ transitionDelay: `${Math.min(i * 80, 220)}ms` }}>
                <div className="flex gap-0.5 mb-3 text-accent">
                  {[0, 1, 2, 3, 4].map(s => (
                    <svg key={s} className="w-4 h-4" viewBox="0 0 24 24" fill="currentColor"><path d="M12 2l2.9 6.3 6.9.7-5.1 4.6 1.4 6.8L12 17.8 5.9 20.4l1.4-6.8L2.2 9l6.9-.7z" /></svg>
                  ))}
                </div>
                <blockquote className="text-sm text-text-primary/90 leading-relaxed mb-4">«{r.text}»</blockquote>
                <figcaption className="flex items-center gap-3">
                  <span className="w-9 h-9 rounded-full bg-brand/15 border border-brand/25 text-brand flex items-center justify-center text-sm font-semibold">{r.name[0]}</span>
                  <span className="text-sm"><b className="font-semibold">{r.name}</b><span className="text-faint"> · {r.role}</span></span>
                </figcaption>
              </figure>
            ))}
          </div>
        </div>
      </section>

      {/* ─── FAQ ─── */}
      <section className="relative py-20 md:py-28">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/12 to-transparent" />
        <div className="max-w-3xl mx-auto px-6">
          <div className="text-center mb-12 reveal">
            <p className="eyebrow mb-4">Вопросы</p>
            <h2 className="text-3xl md:text-4xl font-semibold">Коротко о главном</h2>
          </div>
          <div className="space-y-3">
            {faqs.map((f, i) => {
              const open = openFaq === i
              return (
                <div key={f.q} className="card overflow-hidden reveal" style={{ transitionDelay: `${Math.min(i * 60, 200)}ms` }}>
                  <button onClick={() => setOpenFaq(open ? -1 : i)} className="w-full flex items-center justify-between gap-4 px-6 py-5 text-left" aria-expanded={open}>
                    <span className="font-semibold text-[15px]">{f.q}</span>
                    <svg className={`w-5 h-5 text-brand flex-shrink-0 transition-transform duration-300 ${open ? 'rotate-45' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} strokeLinecap="round"><path d="M12 5v14M5 12h14" /></svg>
                  </button>
                  <div className="grid transition-all duration-300 ease-out" style={{ gridTemplateRows: open ? '1fr' : '0fr' }}>
                    <div className="overflow-hidden">
                      <p className="px-6 pb-5 text-sm text-text-secondary leading-relaxed">{f.a}</p>
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      </section>

      {/* ─── Downloads ─── */}
      <section className="relative py-20 md:py-28">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/12 to-transparent" />
        <div className="max-w-4xl mx-auto px-6 text-center">
          <div className="reveal">
            <p className="eyebrow mb-4">Приложения</p>
            <h2 className="text-3xl md:text-4xl font-semibold mb-4">На всех платформах</h2>
            <p className="text-text-secondary max-w-lg mx-auto mb-12">Создайте аккаунт — и подключитесь на своём устройстве за пару минут.</p>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 max-w-2xl mx-auto">
            {['Windows', 'Android', 'iOS', 'macOS'].map((name, i) => (
              <Link key={name} to={isLoggedIn ? '/connections' : '/register'} className="group card card-hover p-6 reveal" style={{ transitionDelay: `${Math.min(i * 70, 220)}ms` }}>
                <div className="w-11 h-11 mx-auto rounded-xl bg-brand/12 border border-brand/20 text-brand flex items-center justify-center mb-3 transition-transform duration-300 group-hover:scale-110">
                  <IconDevices className="w-6 h-6" />
                </div>
                <p className="text-sm font-medium">{name}</p>
              </Link>
            ))}
          </div>
        </div>
      </section>

      {/* ─── Refer a friend ─── */}
      {refTerms?.enabled && refTerms.friend_kop > 0 && (
        <section className="relative py-16 md:py-20">
          <div className="max-w-3xl mx-auto px-6">
            <div className="card-feat rounded-[26px] p-8 md:p-10 text-center reveal">
              <p className="eyebrow mb-3">Приведи друга</p>
              <h2 className="serif text-2xl md:text-3xl font-semibold mb-3">
                По {Math.round(refTerms.referrer_kop / 100)} ₽ каждому
              </h2>
              <p className="text-text-secondary max-w-xl mx-auto">
                Поделитесь ссылкой из кабинета. Друг оплатит подписку — вам и ему придёт по {Math.round(refTerms.friend_kop / 100)} ₽ на кошелёк HiEarth.
              </p>
            </div>
          </div>
        </section>
      )}

      {/* ─── CTA band ─── */}
      <section className="relative py-24 md:py-32 overflow-hidden">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/20 to-transparent" />
        <Fireflies density={16000} />
        <div className="pointer-events-none absolute inset-0 bg-gradient-to-b from-transparent via-brand/5 to-transparent" />
        <div className="relative max-w-2xl mx-auto px-6 text-center">
          <h2 className="serif text-3xl md:text-5xl font-semibold reveal">Войдите в тихий лес</h2>
          <p className="text-text-secondary max-w-lg mx-auto mt-5 mb-9 reveal" style={{ transitionDelay: '80ms' }}>
            Анонимный ID за один клик. Бесплатный старт, без карты и обязательств.
          </p>
          <div className="reveal" style={{ transitionDelay: '160ms' }}>
            <Link to={isLoggedIn ? '/dashboard' : '/register'} className="btn-primary">
              {isLoggedIn ? 'В кабинет' : 'Начать бесплатно'} <Arrow />
            </Link>
          </div>
        </div>
      </section>
    </>
  )
}
