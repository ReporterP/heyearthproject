import { Link } from 'react-router-dom'

export default function SiteFooter() {
  return (
    <footer className="relative border-t border-white/5 overflow-hidden">
      {/* Subtle gradient glow at top */}
      <div className="absolute top-0 left-1/2 -translate-x-1/2 w-96 h-px bg-gradient-to-r from-transparent via-brand/40 to-transparent" />

      <div className="max-w-6xl mx-auto px-6 py-12">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-10 md:gap-8">
          {/* Brand */}
          <div>
            <Link to="/" className="inline-flex items-center gap-2 mb-4">
              <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-brand-light to-accent flex items-center justify-center">
                <span className="text-xs font-black text-white">H</span>
              </div>
              <span className="text-lg font-bold tracking-tight">
                <span className="text-brand-light">Hey</span>Earth
              </span>
            </Link>
            <p className="text-sm text-text-secondary leading-relaxed max-w-xs">
              Устойчивый интернет-доступ с эко-маршрутизацией для пользователей и бизнеса.
            </p>
          </div>

          {/* Links */}
          <div className="flex gap-12 md:justify-center">
            <div>
              <p className="text-xs font-semibold uppercase tracking-wider text-text-secondary mb-3">Продукт</p>
              <ul className="space-y-2">
                <li><Link to="/" className="text-sm text-text-secondary hover:text-text-primary transition-colors duration-200">Для пользователей</Link></li>
                <li><Link to="/business" className="text-sm text-text-secondary hover:text-text-primary transition-colors duration-200">Для бизнеса</Link></li>
                <li><a href="/#plans" className="text-sm text-text-secondary hover:text-text-primary transition-colors duration-200">Тарифы</a></li>
              </ul>
            </div>
            <div>
              <p className="text-xs font-semibold uppercase tracking-wider text-text-secondary mb-3">Аккаунт</p>
              <ul className="space-y-2">
                <li><Link to="/login" className="text-sm text-text-secondary hover:text-text-primary transition-colors duration-200">Войти</Link></li>
                <li><Link to="/register" className="text-sm text-text-secondary hover:text-text-primary transition-colors duration-200">Регистрация</Link></li>
                <li><Link to="/dashboard" className="text-sm text-text-secondary hover:text-text-primary transition-colors duration-200">Кабинет</Link></li>
              </ul>
            </div>
          </div>

          {/* Contact / Status */}
          <div className="md:text-right">
            <p className="text-xs font-semibold uppercase tracking-wider text-text-secondary mb-3">Статус</p>
            <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full glass text-xs text-text-secondary">
              <span className="w-2 h-2 rounded-full bg-green-400 animate-pulse" />
              Все системы работают
            </div>
          </div>
        </div>

        {/* Bottom bar */}
        <div className="mt-10 pt-6 border-t border-white/5 flex flex-col md:flex-row items-center justify-between gap-3">
          <p className="text-xs text-text-secondary">
            &copy; {new Date().getFullYear()} HeyEarth. Все права защищены.
          </p>
          <p className="text-xs text-text-secondary/50">
            Оптимизация трафика для устойчивого интернета
          </p>
        </div>
      </div>
    </footer>
  )
}
