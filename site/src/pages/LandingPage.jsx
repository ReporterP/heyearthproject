import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'

const features = [
  {
    icon: '🌿',
    title: 'Эко-маршрутизация',
    desc: 'Оптимизированные маршруты трафика снижают энергопотребление и ускоряют доступ к ресурсам.',
    color: 'from-emerald-500/20 to-green-500/10',
    iconBg: 'from-emerald-500/30 to-green-600/20',
  },
  {
    icon: '📱',
    title: 'Все устройства',
    desc: 'Android, Windows, iOS, macOS, Linux. Один аккаунт — все платформы.',
    color: 'from-cyan-500/20 to-blue-500/10',
    iconBg: 'from-cyan-500/30 to-blue-600/20',
  },
  {
    icon: '🛡️',
    title: 'Безопасность',
    desc: 'Шифрование трафика и защита данных на всех этапах маршрутизации.',
    color: 'from-violet-500/20 to-purple-500/10',
    iconBg: 'from-violet-500/30 to-purple-600/20',
  },
  {
    icon: '⚡',
    title: 'Высокая скорость',
    desc: 'Минимальная задержка благодаря оптимизированной сети маршрутизации.',
    color: 'from-amber-500/20 to-orange-500/10',
    iconBg: 'from-amber-500/30 to-orange-600/20',
  },
]

const steps = [
  { num: '01', title: 'Регистрация', desc: 'Получите уникальный HeYearth ID за один клик. Без email, без номера телефона.' },
  { num: '02', title: 'Подключение', desc: 'Установите приложение или настройте эко-маршрутизацию на любом устройстве за пару кликов.' },
  { num: '03', title: 'Готово', desc: 'Пользуйтесь быстрым и оптимизированным доступом к интернету без ограничений.' },
]

// Plans loaded dynamically from API in component

const CheckIcon = () => (
  <svg className="w-4 h-4 mt-0.5 text-accent flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
  </svg>
)

const stats = [
  { value: '99.9%', label: 'Аптайм', icon: '🟢' },
  { value: '<15мс', label: 'Задержка', icon: '🚀' },
  { value: '50+', label: 'Локаций', icon: '🌍' },
]

function formatTraffic(bytes) {
  if (!bytes) return 'Безлимит'
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(0)} ГБ` : `${(bytes / (1024 * 1024)).toFixed(0)} МБ`
}

function formatDuration(days) {
  if (days >= 365) return `${Math.round(days / 365)} год`
  if (days >= 30) return `${Math.round(days / 30)} мес.`
  return `${days} дн.`
}

export default function LandingPage() {
  const [plans, setPlans] = useState([])
  useEffect(() => {
    fetch('/api/v1/plans').then(r => r.json()).then(d => setPlans(Array.isArray(d) ? d.filter(p => p.price_rub > 0) : [])).catch(() => {})
  }, [])
  return (
    <>
      {/* ─── Hero ─── */}
      <section className="relative overflow-hidden pt-24 pb-32 md:pt-32 md:pb-40">
        {/* Animated gradient background */}
        <div className="absolute inset-0">
          <div className="absolute inset-0 bg-gradient-to-br from-brand/15 via-surface to-accent/10 animate-gradient" />
          {/* Orbs */}
          <div className="absolute top-20 left-[10%] w-72 h-72 rounded-full bg-brand/15 blur-[100px] animate-pulse-glow" />
          <div className="absolute bottom-20 right-[10%] w-80 h-80 rounded-full bg-accent/10 blur-[120px] animate-pulse-glow delay-700" />
          <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[600px] h-[600px] rounded-full bg-brand/5 blur-[200px]" />
          {/* Grid pattern overlay */}
          <div
            className="absolute inset-0 opacity-[0.03]"
            style={{
              backgroundImage: 'linear-gradient(rgba(255,255,255,0.1) 1px, transparent 1px), linear-gradient(90deg, rgba(255,255,255,0.1) 1px, transparent 1px)',
              backgroundSize: '60px 60px',
            }}
          />
        </div>

        <div className="relative max-w-5xl mx-auto px-6 text-center">
          {/* Badge */}
          <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-full glass text-xs font-medium text-text-secondary mb-8 animate-fade-in">
            <span className="w-2 h-2 rounded-full bg-green-400 animate-pulse" />
            Устойчивый интернет нового поколения
          </div>

          <h1 className="text-4xl sm:text-5xl md:text-7xl font-extrabold tracking-tight leading-[1.08] animate-fade-in-up">
            Уменьшите свой{' '}
            <br className="hidden md:block" />
            <span className="text-transparent bg-clip-text bg-gradient-to-r from-brand-light via-accent to-brand-light animate-gradient bg-[length:200%_auto]">
              цифровой углеродный след
            </span>
          </h1>

          <p className="mt-6 md:mt-8 text-base md:text-lg text-text-secondary max-w-2xl mx-auto leading-relaxed animate-fade-in-up delay-200">
            HeYearth — устойчивый интернет-доступ с эко-маршрутизацией.
            Оптимизируйте трафик, снижайте энергопотребление и пользуйтесь
            быстрым доступом к ресурсам на всех устройствах.
          </p>

          <div className="mt-10 flex flex-wrap justify-center gap-4 animate-fade-in-up delay-300">
            <Link
              to="/register"
              className="group relative px-8 py-3.5 rounded-xl font-semibold text-white overflow-hidden transition-all duration-300 hover:shadow-xl hover:shadow-brand/25 hover:-translate-y-0.5"
            >
              <span className="absolute inset-0 bg-gradient-to-r from-brand to-brand-dark" />
              <span className="absolute inset-0 bg-gradient-to-r from-brand-light to-accent opacity-0 group-hover:opacity-100 transition-opacity duration-500" />
              <span className="relative z-10 flex items-center gap-2">
                Начать
                <svg className="w-4 h-4 group-hover:translate-x-1 transition-transform" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                </svg>
              </span>
            </Link>
            <a
              href="#plans"
              className="px-8 py-3.5 rounded-xl font-semibold glass hover:bg-white/10 text-text-secondary hover:text-text-primary transition-all duration-300"
            >
              Тарифы
            </a>
          </div>

          {/* Stats */}
          <div className="mt-20 grid grid-cols-3 max-w-lg mx-auto animate-fade-in-up delay-500">
            {stats.map((s, i) => (
              <div key={s.label} className={`text-center ${i !== 0 ? 'border-l border-white/10' : ''}`}>
                <p className="text-2xl md:text-3xl font-extrabold tracking-tight">{s.value}</p>
                <p className="text-xs text-text-secondary mt-1 font-medium">{s.label}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ─── Features ─── */}
      <section className="relative py-24 md:py-32">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-white/10 to-transparent" />

        <div className="max-w-6xl mx-auto px-6">
          <div className="text-center mb-16 animate-fade-in">
            <p className="text-sm font-semibold text-accent uppercase tracking-wider mb-3">Преимущества</p>
            <h2 className="text-3xl md:text-4xl font-bold">Почему HeYearth</h2>
          </div>

          <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-5">
            {features.map((f, i) => (
              <div
                key={f.title}
                className={`group relative p-6 rounded-2xl glass-card hover:bg-white/[0.06] transition-all duration-500 hover:-translate-y-1 hover:shadow-lg hover:shadow-brand/5 animate-fade-in-up delay-${(i + 1) * 100}`}
              >
                <div className={`absolute top-0 right-0 w-24 h-24 bg-gradient-to-bl ${f.color} rounded-bl-[80px] rounded-tr-2xl opacity-0 group-hover:opacity-100 transition-opacity duration-500`} />

                <div className="relative z-10">
                  <div className={`w-12 h-12 rounded-xl bg-gradient-to-br ${f.iconBg} flex items-center justify-center text-2xl mb-4 group-hover:scale-110 transition-transform duration-300`}>
                    {f.icon}
                  </div>
                  <h3 className="text-base font-semibold mb-2">{f.title}</h3>
                  <p className="text-sm text-text-secondary leading-relaxed">{f.desc}</p>
                </div>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ─── How it works ─── */}
      <section className="relative py-24 md:py-32">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-white/10 to-transparent" />
        <div className="absolute top-1/2 left-0 w-96 h-96 bg-accent/5 rounded-full blur-[150px] pointer-events-none" />

        <div className="max-w-5xl mx-auto px-6">
          <div className="text-center mb-16 animate-fade-in">
            <p className="text-sm font-semibold text-accent uppercase tracking-wider mb-3">Начало работы</p>
            <h2 className="text-3xl md:text-4xl font-bold">Три простых шага</h2>
          </div>

          <div className="grid md:grid-cols-3 gap-8">
            {steps.map((s, i) => (
              <div key={s.num} className={`relative animate-fade-in-up delay-${(i + 1) * 200}`}>
                {/* Connector line */}
                {i < steps.length - 1 && (
                  <div className="hidden md:block absolute top-8 left-[calc(50%+40px)] w-[calc(100%-40px)] h-px bg-gradient-to-r from-brand/30 to-transparent" />
                )}
                <div className="text-center">
                  <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl glass-strong text-2xl font-extrabold text-brand-light mb-5 relative">
                    {s.num}
                    <div className="absolute inset-0 rounded-2xl bg-gradient-to-br from-brand/10 to-accent/5" />
                  </div>
                  <h3 className="text-lg font-bold mb-2">{s.title}</h3>
                  <p className="text-sm text-text-secondary leading-relaxed max-w-xs mx-auto">{s.desc}</p>
                </div>
              </div>
            ))}
          </div>

          <div className="text-center mt-12 animate-fade-in-up delay-600">
            <Link
              to="/register"
              className="inline-flex items-center gap-2 px-6 py-3 rounded-xl glass hover:bg-brand/10 text-brand-light font-semibold transition-all duration-300 hover:-translate-y-0.5"
            >
              Создать аккаунт за 10 секунд
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
              </svg>
            </Link>
          </div>
        </div>
      </section>

      {/* ─── Plans ─── */}
      <section id="plans" className="relative py-24 md:py-32">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-white/10 to-transparent" />
        <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[600px] h-[400px] bg-brand/5 rounded-full blur-[120px] pointer-events-none" />

        <div className="relative max-w-4xl mx-auto px-6">
          <div className="text-center mb-16 animate-fade-in">
            <p className="text-sm font-semibold text-accent uppercase tracking-wider mb-3">Тарифы</p>
            <h2 className="text-3xl md:text-4xl font-bold">Простые и прозрачные цены</h2>
            <p className="mt-4 text-text-secondary max-w-lg mx-auto">
              Без скрытых платежей. Оплата помесячно, отмена в любой момент.
            </p>
          </div>

          <div className={`grid gap-6 md:gap-8 ${plans.length >= 3 ? 'md:grid-cols-3' : 'md:grid-cols-2'}`}>
            {plans.map((p, i) => {
              const highlighted = i === Math.min(Math.floor(plans.length / 2), plans.length - 1)
              const planFeatures = []
              planFeatures.push(p.description)
              planFeatures.push(`Трафик: ${formatTraffic(p.traffic_limit_bytes)}`)
              planFeatures.push(`Срок: ${formatDuration(p.duration_days)}`)
              const pf = typeof p.features === 'string' ? JSON.parse(p.features || '{}') : (p.features || {})
              if (pf.traffic_masking) planFeatures.push('Маскировка трафика')
              if (pf.priority_support) planFeatures.push('Приоритетная поддержка')
              return (
              <div
                key={p.id}
                className={`group relative p-8 rounded-2xl transition-all duration-500 hover:-translate-y-1 animate-fade-in-up ${
                  highlighted
                    ? 'glass-strong border border-brand/30 shadow-xl shadow-brand/10 glow-brand'
                    : 'glass-card hover:bg-white/[0.06]'
                }`}
                style={{ animationDelay: `${(i + 1) * 200}ms` }}
              >
                {highlighted && (
                  <div className="absolute -top-3.5 left-1/2 -translate-x-1/2">
                    <span className="px-4 py-1 rounded-full bg-gradient-to-r from-brand to-brand-light text-xs font-bold text-white shadow-lg shadow-brand/25">
                      Популярный
                    </span>
                  </div>
                )}

                <h3 className="text-xl font-bold mb-1">{p.name}</h3>
                <div className="flex items-baseline gap-1 mb-6">
                  <span className="text-5xl font-extrabold tracking-tight">{p.price_rub}</span>
                  <span className="text-text-secondary text-sm">&thinsp;&#8381; / {formatDuration(p.duration_days)}</span>
                </div>

                <ul className="space-y-3 mb-8">
                  {planFeatures.map(feat => (
                    <li key={feat} className="flex items-start gap-3 text-sm text-text-secondary">
                      <CheckIcon />
                      <span>{feat}</span>
                    </li>
                  ))}
                </ul>

                <Link
                  to="/payment"
                  className={`block text-center px-6 py-3.5 rounded-xl font-semibold transition-all duration-300 ${
                    highlighted
                      ? 'bg-gradient-to-r from-brand to-brand-dark text-white hover:shadow-lg hover:shadow-brand/25 hover:-translate-y-0.5'
                      : 'glass hover:bg-white/10 text-text-primary'
                  }`}
                >
                  Выбрать
                </Link>
              </div>
            )})}
          </div>
        </div>
      </section>

      {/* ─── Downloads ─── */}
      <section className="relative py-24 md:py-32">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-white/10 to-transparent" />

        <div className="max-w-4xl mx-auto px-6 text-center">
          <div className="animate-fade-in">
            <p className="text-sm font-semibold text-accent uppercase tracking-wider mb-3">Приложения</p>
            <h2 className="text-3xl md:text-4xl font-bold mb-4">Доступно на всех платформах</h2>
            <p className="text-text-secondary max-w-lg mx-auto mb-12">
              Скачайте приложение HeYearth для вашей платформы и подключитесь за пару минут.
            </p>
          </div>

          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 max-w-2xl mx-auto animate-fade-in-up delay-200">
            {[
              { name: 'Windows', icon: '🪟' },
              { name: 'Android', icon: '🤖' },
              { name: 'iOS', icon: '📱' },
              { name: 'macOS', icon: '💻' },
            ].map(p => (
              <div key={p.name} className="group p-5 rounded-2xl glass-card hover:bg-white/[0.06] transition-all duration-300 hover:-translate-y-1 cursor-pointer">
                <div className="text-3xl mb-2 group-hover:scale-110 transition-transform duration-300">{p.icon}</div>
                <p className="text-sm font-medium">{p.name}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ─── Business CTA ─── */}
      <section className="relative py-24 md:py-32 overflow-hidden">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-white/10 to-transparent" />
        <div className="absolute inset-0 bg-gradient-to-b from-transparent via-brand/5 to-transparent pointer-events-none" />

        <div className="relative max-w-3xl mx-auto px-6 text-center">
          <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-full glass text-xs font-medium text-text-secondary mb-6 animate-fade-in">
            <svg className="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 21h16.5M4.5 3h15M5.25 3v18m13.5-18v18M9 6.75h1.5m-1.5 3h1.5m-1.5 3h1.5m3-6H15m-1.5 3H15m-1.5 3H15M9 21v-3.375c0-.621.504-1.125 1.125-1.125h3.75c.621 0 1.125.504 1.125 1.125V21" />
            </svg>
            Корпоративные решения
          </div>

          <h2 className="text-3xl md:text-4xl font-bold mb-4 animate-fade-in-up">
            Эко-маршрутизация{' '}
            <span className="text-transparent bg-clip-text bg-gradient-to-r from-brand-light to-accent">
              для бизнеса
            </span>
          </h2>
          <p className="text-text-secondary max-w-xl mx-auto mb-10 animate-fade-in-up delay-200">
            Корпоративная эко-оптимизация трафика. Своя панель управления, контроль сотрудников, гибкие тарифы. Без предоплаты.
          </p>

          <Link
            to="/business"
            className="group inline-flex items-center gap-2 px-8 py-3.5 rounded-xl glass hover:bg-brand/10 text-brand-light font-semibold transition-all duration-300 hover:-translate-y-0.5 animate-fade-in-up delay-300"
          >
            Подробнее
            <svg className="w-4 h-4 group-hover:translate-x-1 transition-transform" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
            </svg>
          </Link>
        </div>
      </section>
    </>
  )
}
