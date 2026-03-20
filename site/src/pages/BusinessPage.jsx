import { Link } from 'react-router-dom'

const advantages = [
  { icon: '\u{1F3E2}', title: 'Своя панель управления', desc: 'Управляйте эко-маршрутизацией для всех сотрудников. Полный контроль и аналитика.' },
  { icon: '\u{1F527}', title: 'Свои или наши серверы', desc: 'Подключите собственную инфраструктуру или используйте наши оптимизированные узлы.' },
  { icon: '\u{1F4B0}', title: 'Оплата по потреблению', desc: 'Кошелёк с ежемесячным списанием. Платите только за то, что используете.' },
  { icon: '\u{1F331}', title: 'Корпоративная устойчивость', desc: 'Снизьте цифровой углеродный след компании. Коды доступа для сотрудников без регистрации.' },
]

const pricing = [
  { label: 'Пользователь', price: '250 \u20BD / мес' },
  { label: 'Использование наших серверов', price: '1 000 \u20BD / мес' },
  { label: 'Свои серверы (до 5 включены)', price: '+100 \u20BD / доп. сервер' },
  { label: 'Премиум для пользователя', price: '+50 \u20BD / мес' },
]

export default function BusinessPage() {
  return (
    <>
      {/* Hero */}
      <section className="pt-24 pb-16">
        <div className="max-w-4xl mx-auto px-6 text-center">
          <h1 className="text-4xl md:text-5xl font-extrabold tracking-tight">
            Устойчивый доступ <span className="text-transparent bg-clip-text bg-gradient-to-r from-brand-light to-accent">для бизнеса</span>
          </h1>
          <p className="mt-6 text-lg text-text-secondary max-w-2xl mx-auto">
            Оптимизируйте корпоративный трафик с заботой об экологии. Управляйте доступом сотрудников из удобной панели.
            Прозрачные тарифы — без скрытых платежей.
          </p>
        </div>
      </section>

      {/* Advantages */}
      <section className="py-16">
        <div className="max-w-6xl mx-auto px-6 grid md:grid-cols-2 gap-8">
          {advantages.map(a => (
            <div key={a.title} className="p-8 rounded-2xl bg-surface-card border border-white/5 hover:border-brand/30 transition">
              <div className="text-3xl mb-4">{a.icon}</div>
              <h3 className="text-xl font-semibold mb-2">{a.title}</h3>
              <p className="text-text-secondary leading-relaxed">{a.desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Pricing */}
      <section className="py-16 bg-surface-alt/50">
        <div className="max-w-3xl mx-auto px-6">
          <h2 className="text-2xl font-bold text-center mb-10">Тарификация</h2>
          <div className="space-y-4 text-sm">
            {pricing.map(row => (
              <div key={row.label} className="flex justify-between p-4 rounded-xl bg-surface-card border border-white/5">
                <span className="text-text-secondary">{row.label}</span>
                <span className="font-semibold">{row.price}</span>
              </div>
            ))}
          </div>
          <p className="text-xs text-text-secondary text-center mt-4">Цены настраиваются индивидуально. Указаны базовые значения.</p>
        </div>
      </section>

      {/* CTA */}
      <section className="py-20">
        <div className="max-w-3xl mx-auto px-6 text-center">
          <h2 className="text-3xl font-bold mb-4">Готовы начать?</h2>
          <p className="text-text-secondary mb-8">
            Выберите параметры, укажите почту — и сразу получите доступ в панель управления. Без предоплаты.
          </p>
          <Link to="/business/signup" className="inline-block px-8 py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition shadow-lg shadow-brand/25">
            Подключить бизнес
          </Link>
        </div>
      </section>
    </>
  )
}
