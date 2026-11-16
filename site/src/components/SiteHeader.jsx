import { useEffect, useRef, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useSiteInfo from '../hooks/useSiteInfo.js'
import useAccount from '../account/useAccount.js'
import Logo from './Logo.jsx'
import Button from './Button.jsx'
import { IdChip } from './Nomerok.jsx'

// SiteHeader (spec 02 §28) + mobile drawer (§29).
//   variant 'site'     — public pages: anchors, «Помощь», «Для бизнеса» (b2b), login / start / cabinet;
//           'business' — the B2B landing: «Для людей», «Для бизнеса», «Помощь», «Подключить бизнес»;
//           'minimal'  — logo only (register, login).
//   transparentTop — the landing: no background/line at the very top (`is-top`).
export default function SiteHeader({ variant = 'site', transparentTop = false }) {
  const { isLoggedIn } = useAuth()
  const { info } = useSiteInfo()
  const { pathname, hash } = useLocation()
  const [open, setOpen] = useState(false)
  const [atTop, setAtTop] = useState(true)
  const btn = useRef(null)
  const drawer = useRef(null)
  const { data } = useAccount(isLoggedIn && variant !== 'minimal' ? ['profile'] : [], { optional: ['profile'] })
  const userId = data?.profile?.user_id

  useEffect(() => {
    if (!transparentTop) return undefined
    const on = () => setAtTop(window.scrollY < 8)
    on()
    window.addEventListener('scroll', on, { passive: true })
    return () => window.removeEventListener('scroll', on)
  }, [transparentTop])

  // close on navigation, Esc, and when the screen grows past the phone width
  useEffect(() => { setOpen(false) }, [pathname, hash])
  useEffect(() => {
    if (!open) return undefined
    const onKey = (e) => { if (e.key === 'Escape') { setOpen(false); btn.current?.focus() } }
    const mq = window.matchMedia('(min-width: 641px)')
    const onWide = () => { if (mq.matches) setOpen(false) }
    window.addEventListener('keydown', onKey)
    mq.addEventListener('change', onWide)
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    drawer.current?.querySelector('a,button')?.focus()
    return () => {
      window.removeEventListener('keydown', onKey)
      mq.removeEventListener('change', onWide)
      document.body.style.overflow = prev
    }
  }, [open])

  const links = variant === 'business'
    ? [
        { to: '/', label: 'Для людей' },
        { to: '/business', label: 'Для бизнеса' },
        { to: '/help', label: 'Помощь' },
      ]
    : [
        { to: '/#how', label: 'Как работает' },
        { to: '/#plans', label: 'Тарифы' },
        { to: '/help', label: 'Помощь' },
        ...(info.b2b_enabled ? [{ to: '/business', label: 'Для бизнеса' }] : []),
      ]
  const current = (to) => (to === pathname ? 'page' : undefined)

  if (variant === 'minimal') {
    return (
      <header className="site-nav">
        <div className="container site-nav__in"><Logo /></div>
      </header>
    )
  }

  const startCta = variant === 'business'
    ? <Button variant="primary" size="sm" to="/business/signup">Подключить бизнес</Button>
    : <Button variant="primary" size="sm" to="/register">Начать бесплатно</Button>

  // The drawer is a sibling of the header: the header's backdrop-filter would otherwise become the
  // containing block of the fixed drawer.
  return (
    <>
    <header className={`site-nav ${transparentTop && atTop && !open ? 'is-top' : ''}`}>
      <div className="container site-nav__in">
        <Logo />
        <nav className="nav-links" aria-label="Разделы">
          {links.map((l) => <Link key={l.to} to={l.to} aria-current={current(l.to)}>{l.label}</Link>)}
        </nav>
        <div className="row gap-2">
          {isLoggedIn ? (
            <>
              {userId && <span className="max-sm:hidden"><IdChip id={userId} /></span>}
              <Button variant="secondary" size="sm" to="/dashboard" className="max-sm:hidden">В кабинет</Button>
            </>
          ) : (
            <>
              <Button variant="ghost" size="sm" to="/login" className="max-sm:hidden">Войти</Button>
              <span className="max-sm:hidden">{startCta}</span>
            </>
          )}
          <button
            ref={btn}
            type="button"
            className={`menu-btn sm:hidden ${open ? 'is-open' : ''}`}
            aria-expanded={open}
            aria-controls="drawer"
            aria-label={open ? 'Закрыть меню' : 'Меню'}
            onClick={() => setOpen((o) => !o)}
          >
            <span /><span />
          </button>
        </div>
      </div>
    </header>
      <div id="drawer" ref={drawer} className={`drawer sm:hidden ${open ? 'is-open' : ''}`} aria-hidden={!open}>
        {links.map((l) => (
          <Link key={l.to} to={l.to} aria-current={current(l.to)} tabIndex={open ? 0 : -1} onClick={() => setOpen(false)}>{l.label}</Link>
        ))}
        <div className="drawer__foot">
          {isLoggedIn ? (
            <>
              {userId && <IdChip id={userId} />}
              <Button variant="primary" block size="lg" to="/dashboard" tabIndex={open ? 0 : -1}>В кабинет</Button>
            </>
          ) : (
            <>
              <Button variant="secondary" block size="lg" to="/login" tabIndex={open ? 0 : -1}>Войти</Button>
              <Button variant="primary" block size="lg" to={variant === 'business' ? '/business/signup' : '/register'} tabIndex={open ? 0 : -1}>
                {variant === 'business' ? 'Подключить бизнес' : 'Начать бесплатно'}
              </Button>
            </>
          )}
        </div>
      </div>
    </>
  )
}
