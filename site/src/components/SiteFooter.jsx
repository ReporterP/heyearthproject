import { Link } from 'react-router-dom'
import useLegalInfo from '../hooks/useLegalInfo.js'
import useSiteInfo from '../hooks/useSiteInfo.js'

const LeafMark = ({ className = 'w-4 h-4' }) => (
  <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 21c-4.5-1-8.5-4.5-8.5-10.5C9 10.5 12 13.5 12 17M12 21c4.5-1 8.5-4.5 8.5-10.5C15 10.5 12 13.5 12 17M12 21V8" />
  </svg>
)

export default function SiteFooter() {
  const legal = useLegalInfo()
  const { info } = useSiteInfo()
  const legalEmail = legal.legal_email || ''
  const legalInn = legal.legal_inn || ''

  return (
    <footer className="relative border-t border-brand/10 overflow-hidden">
      <div className="absolute top-0 left-1/2 -translate-x-1/2 w-96 h-px bg-gradient-to-r from-transparent via-brand/50 to-transparent" />

      <div className="max-w-6xl mx-auto px-6 py-14">
        <div className="grid grid-cols-1 md:grid-cols-4 gap-10 md:gap-8">
          {/* Brand */}
          <div>
            <Link to="/" className="inline-flex items-center gap-2 mb-4">
              <span className="w-8 h-8 rounded-lg bg-brand/12 border border-brand/25 text-brand flex items-center justify-center">
                <LeafMark className="w-4 h-4" />
              </span>
              <span className="serif text-lg font-semibold text-text-primary">HiEarth</span>
            </Link>
            <p className="text-sm text-text-secondary leading-relaxed max-w-xs">
              Устойчивый интернет-доступ с эко-маршрутизацией трафика. Тихо, приватно, без лишнего шума.
            </p>
            {legal.legal_company_name && <p className="text-xs text-text-secondary mt-3">{legal.legal_company_name}</p>}
            {legalEmail && (
              <p className="text-xs text-text-secondary">
                <a href={`mailto:${legalEmail}`} className="hover:text-brand transition">{legalEmail}</a>
              </p>
            )}
            {legalInn && <p className="text-xs text-faint">ИНН: {legalInn}</p>}
          </div>

          {/* Product links */}
          <div>
            <p className="text-xs font-semibold uppercase tracking-wider text-faint mb-3">Продукт</p>
            <ul className="space-y-2">
              <li><Link to="/" className="text-sm text-text-secondary hover:text-text-primary transition">Главная</Link></li>
              <li><Link to="/payment" className="text-sm text-text-secondary hover:text-text-primary transition">Тарифы</Link></li>
              {info.b2b_enabled && <li><Link to="/business" className="text-sm text-text-secondary hover:text-text-primary transition">Для бизнеса</Link></li>}
              <li><Link to="/help" className="text-sm text-text-secondary hover:text-text-primary transition">Помощь</Link></li>
              <li><Link to="/dashboard" className="text-sm text-text-secondary hover:text-text-primary transition">Личный кабинет</Link></li>
            </ul>
          </div>

          {/* Legal links */}
          <div>
            <p className="text-xs font-semibold uppercase tracking-wider text-faint mb-3">Документы</p>
            <ul className="space-y-2">
              <li><Link to="/offer" className="text-sm text-text-secondary hover:text-text-primary transition">Договор-оферта</Link></li>
              <li><Link to="/privacy" className="text-sm text-text-secondary hover:text-text-primary transition">Политика конфиденциальности</Link></li>
              <li><Link to="/refund" className="text-sm text-text-secondary hover:text-text-primary transition">Условия возврата</Link></li>
            </ul>
          </div>

          {/* Payment */}
          <div className="md:text-right">
            <p className="text-xs font-semibold uppercase tracking-wider text-faint mb-3">Оплата</p>
            <div className="flex flex-wrap gap-2 md:justify-end mb-4">
              {['Visa', 'MasterCard', 'МИР', 'СБП', 'ЮMoney'].map(m => (
                <span key={m} className="px-2 py-1 rounded bg-white/5 border border-brand/8 text-[10px] text-text-secondary font-medium">{m}</span>
              ))}
            </div>
            <a href="https://yookassa.ru" target="_blank" rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 text-xs text-text-secondary hover:text-text-primary transition">
              Оплата через ЮKassa
            </a>
          </div>
        </div>

        {/* Bottom bar */}
        <div className="mt-10 pt-6 border-t border-brand/10 flex flex-col md:flex-row items-center justify-between gap-3">
          <p className="text-xs text-faint">
            &copy; {new Date().getFullYear()} HiEarth.{legal.legal_company_name ? ` ${legal.legal_company_name}.` : ''} Все права защищены.
          </p>
          <div className="flex gap-4">
            <Link to="/privacy" className="text-xs text-faint hover:text-text-secondary transition">Конфиденциальность</Link>
            <Link to="/offer" className="text-xs text-faint hover:text-text-secondary transition">Оферта</Link>
            <Link to="/refund" className="text-xs text-faint hover:text-text-secondary transition">Возврат</Link>
          </div>
        </div>
      </div>
    </footer>
  )
}
