import { Link } from 'react-router-dom'
import Fireflies from '../components/Fireflies.jsx'
import useReveal from '../hooks/useReveal.js'

const Arrow = ({ className = 'w-4 h-4' }) => (
  <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M13 6l6 6-6 6" /></svg>
)

const advantages = [
  {
    icon: (<svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M3.75 3v11.25A2.25 2.25 0 006 16.5h2.25M3.75 3h-1.5m1.5 0h16.5m0 0h1.5m-1.5 0v11.25A2.25 2.25 0 0118 16.5h-2.25m-7.5 0h7.5m-7.5 0l-1 3m8.5-3l1 3m0 0l.5 1.5m-.5-1.5h-9.5m0 0l-.5 1.5m.75-9l3-3 2.148 2.148A12.061 12.061 0 0116.5 7.605" /></svg>),
    title: 'Своя панель управления',
    desc: 'Управляйте эко-маршрутизацией для всех сотрудников. Полный контроль и аналитика.',
  },
  {
    icon: (<svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z" /></svg>),
    title: 'Свои или наши серверы',
    desc: 'Подключите собственную инфраструктуру или используйте наши оптимизированные узлы.',
  },
  {
    icon: (<svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z" /></svg>),
    title: 'Оплата по потреблению',
    desc: 'Кошелёк с ежемесячным списанием. Платите только за то, что используете.',
  },
  {
    icon: (<svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M18 18.72a9.094 9.094 0 003.741-.479 3 3 0 00-4.682-2.72m.94 3.198l.001.031c0 .225-.012.447-.037.666A11.944 11.944 0 0112 21c-2.17 0-4.207-.576-5.963-1.584A6.062 6.062 0 016 18.719m12 0a5.971 5.971 0 00-.941-3.197m0 0A5.995 5.995 0 0012 12.75a5.995 5.995 0 00-5.058 2.772m0 0a3 3 0 00-4.681 2.72 8.986 8.986 0 003.74.477m.94-3.197a5.971 5.971 0 00-.94 3.197M15 6.75a3 3 0 11-6 0 3 3 0 016 0zm6 3a2.25 2.25 0 11-4.5 0 2.25 2.25 0 014.5 0zm-13.5 0a2.25 2.25 0 11-4.5 0 2.25 2.25 0 014.5 0z" /></svg>),
    title: 'Корпоративная устойчивость',
    desc: 'Снизьте цифровой углеродный след компании. Коды доступа для сотрудников без регистрации.',
  },
]

const pricing = [
  { label: 'Пользователь', price: '250 ₽ / мес', icon: (<svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z" /></svg>) },
  { label: 'Наши серверы', price: '1 000 ₽ / мес', icon: (<svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z" /></svg>) },
  { label: 'Свои серверы (до 5 включены)', price: '+100 ₽ / доп.', icon: (<svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" /></svg>) },
  { label: 'Премиум для пользователя', price: '+50 ₽ / мес', icon: (<svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09z" /></svg>) },
]

export default function BusinessPage() {
  useReveal([])
  return (
    <>
      {/* ─── Hero ─── */}
      <section className="relative overflow-hidden pt-20 pb-20 md:pt-28 md:pb-28">
        <Fireflies density={14000} />
        <div className="pointer-events-none absolute inset-0 overflow-hidden">
          <div className="blob-a absolute top-1/4 -right-24 w-96 h-96 rounded-full bg-brand/12 blur-[110px]" />
          <div className="blob-b absolute bottom-1/4 -left-24 w-96 h-96 rounded-full bg-accent/10 blur-[110px]" />
        </div>

        <div className="relative max-w-4xl mx-auto px-6 text-center">
          <div className="eyebrow mb-7 animate-fade-in">
            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7}><path strokeLinecap="round" strokeLinejoin="round" d="M3.75 21h16.5M4.5 3h15M5.25 3v18m13.5-18v18M9 6.75h1.5m-1.5 3h1.5m-1.5 3h1.5m3-6H15m-1.5 3H15m-1.5 3H15M9 21v-3.375c0-.621.504-1.125 1.125-1.125h3.75c.621 0 1.125.504 1.125 1.125V21" /></svg>
            Корпоративные решения
          </div>

          <h1 className="serif text-4xl md:text-6xl font-semibold leading-[1.1] animate-fade-in-up">
            Устойчивый доступ <span className="italic text-brand">для бизнеса</span>
          </h1>

          <p className="mt-7 text-base md:text-lg text-text-secondary max-w-2xl mx-auto leading-relaxed animate-fade-in-up delay-200">
            Оптимизируйте корпоративный трафик с заботой об экологии. Управляйте доступом сотрудников из удобной панели. Прозрачные тарифы — без скрытых платежей.
          </p>

          <div className="mt-9 animate-fade-in-up delay-300 flex flex-wrap items-center justify-center gap-3.5">
            <Link to="/business/signup" className="btn-primary">Подключить бизнес <Arrow /></Link>
            <Link to="/business/invoice" className="btn-ghost">Получить счёт на оплату</Link>
          </div>
        </div>
      </section>

      {/* ─── Advantages ─── */}
      <section className="relative py-20 md:py-28">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/12 to-transparent" />
        <div className="max-w-6xl mx-auto px-6">
          <div className="text-center mb-14 reveal">
            <p className="eyebrow mb-4">Преимущества</p>
            <h2 className="text-3xl md:text-4xl font-semibold">Всё для корпоративного доступа</h2>
          </div>
          <div className="grid md:grid-cols-2 gap-6">
            {advantages.map((a, i) => (
              <div key={a.title} className="group card card-hover p-8 reveal" style={{ transitionDelay: `${Math.min(i * 80, 220)}ms` }}>
                <div className="w-12 h-12 rounded-xl bg-brand/12 border border-brand/20 flex items-center justify-center text-brand mb-5 group-hover:scale-110 transition-transform duration-300">{a.icon}</div>
                <h3 className="serif text-lg font-semibold mb-2">{a.title}</h3>
                <p className="text-text-secondary leading-relaxed">{a.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ─── Pricing ─── */}
      <section className="relative py-20 md:py-28">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/12 to-transparent" />
        <div className="pointer-events-none absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[500px] h-[300px] bg-brand/6 rounded-full blur-[120px]" />
        <div className="relative max-w-3xl mx-auto px-6">
          <div className="text-center mb-12 reveal">
            <p className="eyebrow mb-4">Тарификация</p>
            <h2 className="text-3xl md:text-4xl font-semibold">Прозрачные цены</h2>
            <p className="mt-4 text-text-secondary">Платите только за то, что используете</p>
          </div>
          <div className="space-y-3">
            {pricing.map((row, i) => (
              <div key={row.label} className="group card card-hover flex items-center justify-between p-5 reveal" style={{ transitionDelay: `${Math.min(i * 70, 200)}ms` }}>
                <div className="flex items-center gap-4">
                  <div className="w-9 h-9 rounded-xl bg-brand/12 border border-brand/20 flex items-center justify-center text-brand group-hover:scale-110 transition-transform duration-300">{row.icon}</div>
                  <span className="text-sm text-text-secondary">{row.label}</span>
                </div>
                <span className="font-semibold text-sm whitespace-nowrap tabular-nums">{row.price}</span>
              </div>
            ))}
          </div>
          <p className="text-xs text-faint text-center mt-6">Цены настраиваются индивидуально. Указаны базовые значения.</p>
        </div>
      </section>

      {/* ─── CTA ─── */}
      <section className="relative py-24 md:py-32 overflow-hidden">
        <div className="absolute top-0 inset-x-0 h-px bg-gradient-to-r from-transparent via-brand/20 to-transparent" />
        <Fireflies density={16000} />
        <div className="pointer-events-none absolute inset-0 bg-gradient-to-b from-transparent via-brand/5 to-transparent" />
        <div className="relative max-w-2xl mx-auto px-6 text-center">
          <h2 className="serif text-3xl md:text-5xl font-semibold reveal">Готовы начать?</h2>
          <p className="text-text-secondary max-w-xl mx-auto mt-5 mb-9 reveal" style={{ transitionDelay: '80ms' }}>
            Выберите параметры, укажите почту — и сразу получите доступ в панель управления. Без предоплаты.
          </p>
          <div className="reveal" style={{ transitionDelay: '160ms' }}>
            <Link to="/business/signup" className="btn-primary">Подключить бизнес <Arrow /></Link>
          </div>
        </div>
      </section>
    </>
  )
}
