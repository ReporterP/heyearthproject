import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'

export default function SiteFooter() {
  const [legal, setLegal] = useState({})
  useEffect(() => {
    fetch('/api/v1/legal-info').then(r => r.json()).then(setLegal).catch(() => {})
  }, [])
  const companyShort = legal.legal_company_name ? legal.legal_company_name.replace(/^ИП\s+/, 'ИП ').replace(/\s+\S+$/, ' ' + (legal.legal_company_name.split(' ').pop()?.[0] || '') + '.') : 'ИП Степанов А.В.'
  const legalEmail = legal.legal_email || 'alex_step04@mail.ru'
  const legalInn = legal.legal_inn || ''

  return (
    <footer className="relative border-t border-white/5 overflow-hidden">
      <div className="absolute top-0 left-1/2 -translate-x-1/2 w-96 h-px bg-gradient-to-r from-transparent via-brand/40 to-transparent" />

      <div className="max-w-6xl mx-auto px-6 py-12">
        <div className="grid grid-cols-1 md:grid-cols-4 gap-10 md:gap-8">
          {/* Brand */}
          <div>
            <Link to="/" className="inline-flex items-center gap-2 mb-4">
              <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-brand-light to-accent flex items-center justify-center">
                <span className="text-xs font-black text-white">H</span>
              </div>
              <span className="text-lg font-bold tracking-tight">
                <span className="text-brand-light">Hi</span>Earth
              </span>
            </Link>
            <p className="text-sm text-text-secondary leading-relaxed max-w-xs">
              Устойчивый интернет-доступ с эко-маршрутизацией трафика.
            </p>
            <p className="text-xs text-text-secondary mt-3">{legal.legal_company_name || 'ИП Степанов А.В.'}</p>
            <p className="text-xs text-text-secondary">
              <a href={`mailto:${legalEmail}`} className="hover:text-brand transition">{legalEmail}</a>
            </p>
            {legalInn && <p className="text-xs text-text-secondary">ИНН: {legalInn}</p>}
          </div>

          {/* Product links */}
          <div>
            <p className="text-xs font-semibold uppercase tracking-wider text-text-secondary mb-3">Продукт</p>
            <ul className="space-y-2">
              <li><Link to="/" className="text-sm text-text-secondary hover:text-text-primary transition">Главная</Link></li>
              <li><Link to="/payment" className="text-sm text-text-secondary hover:text-text-primary transition">Тарифы</Link></li>
              <li><Link to="/business" className="text-sm text-text-secondary hover:text-text-primary transition">Для бизнеса</Link></li>
              <li><Link to="/dashboard" className="text-sm text-text-secondary hover:text-text-primary transition">Личный кабинет</Link></li>
            </ul>
          </div>

          {/* Legal links */}
          <div>
            <p className="text-xs font-semibold uppercase tracking-wider text-text-secondary mb-3">Документы</p>
            <ul className="space-y-2">
              <li><Link to="/offer" className="text-sm text-text-secondary hover:text-text-primary transition">Договор-оферта</Link></li>
              <li><Link to="/privacy" className="text-sm text-text-secondary hover:text-text-primary transition">Политика конфиденциальности</Link></li>
              <li><Link to="/refund" className="text-sm text-text-secondary hover:text-text-primary transition">Условия возврата</Link></li>
            </ul>
          </div>

          {/* Payment + Status */}
          <div className="md:text-right">
            <p className="text-xs font-semibold uppercase tracking-wider text-text-secondary mb-3">Оплата</p>
            {/* Payment system logos (text-based for now) */}
            <div className="flex flex-wrap gap-2 md:justify-end mb-4">
              <span className="px-2 py-1 rounded bg-white/5 text-[10px] text-text-secondary font-medium">VISA</span>
              <span className="px-2 py-1 rounded bg-white/5 text-[10px] text-text-secondary font-medium">MasterCard</span>
              <span className="px-2 py-1 rounded bg-white/5 text-[10px] text-text-secondary font-medium">МИР</span>
              <span className="px-2 py-1 rounded bg-white/5 text-[10px] text-text-secondary font-medium">СБП</span>
              <span className="px-2 py-1 rounded bg-white/5 text-[10px] text-text-secondary font-medium">T-Pay</span>
            </div>
            <a href="https://tbank.ru" target="_blank" rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 text-xs text-text-secondary hover:text-text-primary transition">
              <span className="w-5 h-5 rounded bg-yellow-400 flex items-center justify-center text-[10px] font-black text-black">T</span>
              Оплата через Т-Банк
            </a>
            <div className="mt-4">
              <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full glass text-xs text-text-secondary">
                <span className="w-2 h-2 rounded-full bg-green-400 animate-pulse" />
                Все системы работают
              </div>
            </div>
          </div>
        </div>

        {/* Bottom bar */}
        <div className="mt-10 pt-6 border-t border-white/5 flex flex-col md:flex-row items-center justify-between gap-3">
          <p className="text-xs text-text-secondary">
            &copy; {new Date().getFullYear()} HiEarth. {legal.legal_company_name || 'ИП Степанов А.В.'} Все права защищены.
          </p>
          <div className="flex gap-4">
            <Link to="/privacy" className="text-xs text-text-secondary/50 hover:text-text-secondary transition">Конфиденциальность</Link>
            <Link to="/offer" className="text-xs text-text-secondary/50 hover:text-text-secondary transition">Оферта</Link>
            <Link to="/refund" className="text-xs text-text-secondary/50 hover:text-text-secondary transition">Возврат</Link>
          </div>
        </div>
      </div>
    </footer>
  )
}
