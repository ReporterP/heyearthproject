import { useEffect, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount from '../account/useAccount.js'
import useSiteInfo from '../hooks/useSiteInfo.js'
import { apiFetch } from '../api/http.js'
import { useLayer } from '../lib/layers.js'
import Logo from './Logo.jsx'
import Icon from './Icon.jsx'
import Button from './Button.jsx'
import Alert from './Alert.jsx'
import Dialog, { DialogActions } from './Dialog.jsx'
import { IdChip, shortId } from './Nomerok.jsx'
import { isMiniApp } from '../lib/telegram.js'
import { useBadges } from './SupportCount.js'

// AppShell (spec 02 §30): the cabinet's side menu + main column. ≤1024 px the menu becomes a top bar
// (logo · short ID chip · menu button) with the items in a drawer. «Выйти» opens the logout dialog.
const companyCache = new Map() // token → true (member with a dashboard) | false (403/404)

function useCompany(token, enabled) {
  const [has, setHas] = useState(companyCache.get(token) === true)
  useEffect(() => {
    setHas(companyCache.get(token) === true)
    if (!enabled || !token || companyCache.has(token)) return undefined
    let alive = true
    apiFetch('/api/v1/business/me', { token })
      .then(() => { companyCache.set(token, true); if (alive) setHas(true) })
      .catch((e) => { if (e.status === 403 || e.status === 404) companyCache.set(token, false) })
    return () => { alive = false }
  }, [token, enabled])
  return enabled && has
}

export function useLogoutDialog() {
  const [open, setOpen] = useState(false)
  return { open, show: () => setOpen(true), hide: () => setOpen(false) }
}

export function LogoutDialog({ open, onClose, profile }) {
  const { logout } = useAuth()
  const navigate = useNavigate()
  const id = profile?.user_id
  const login = profile?.username
  return (
    <Dialog open={open} onClose={onClose} title="Точно выйти?">
      <p className="muted t-sm" style={{ marginTop: 8 }}>
        Для входа понадобится HiEarth ID{id ? <> <span className="mono">{shortId(id)}</span></> : null}
        {login ? <> или логин <span className="mono">{login}</span></> : null}.
      </p>
      <DialogActions>
        <Button variant="ghost" onClick={onClose} data-autofocus>Остаться</Button>
        <Button variant="danger" onClick={() => { onClose(); logout(); navigate('/') }}>Выйти</Button>
      </DialogActions>
    </Dialog>
  )
}

function useReferralOn() {
  const [on, setOn] = useState(true)
  useEffect(() => {
    let alive = true
    fetch('/api/v1/referral/terms').then((r) => (r.ok ? r.json() : null)).then((t) => { if (alive && t) setOn(!!t.enabled) }).catch(() => {})
    return () => { alive = false }
  }, [])
  return on
}

const ITEMS = [
  { to: '/dashboard', label: 'Обзор', icon: 'home' },
  { to: '/connect', label: 'Подключение', icon: 'power' },
  { to: '/wallet', label: 'Кошелёк', icon: 'wallet' },
  { to: '/payments', label: 'Оплата и подписка', icon: 'receipt' },
  { to: '/dashboard#referral', label: 'Пригласить друга', icon: 'gift', referral: true },
  { to: '/business/dashboard', label: 'Компания', icon: 'building', company: true },
  { to: '/settings', label: 'Настройки', icon: 'settings' },
  { to: '/support', label: 'Поддержка', icon: 'mail', badge: 'support' },
  { to: '/news', label: 'Новости', icon: 'info', badge: 'news' },
  { to: '/help', label: 'Помощь', icon: 'help' },
]

export default function AppShell({ children }) {
  const { token } = useAuth()
  const { info } = useSiteInfo()
  const { pathname, hash } = useLocation()
  const { data } = useAccount(['profile'], { optional: ['profile'] })
  const profile = data?.profile
  const company = useCompany(token, info.b2b_enabled)
  const logoutDlg = useLogoutDialog()
  const [open, setOpen] = useState(false)
  const btn = useRef(null)
  const drawer = useRef(null)

  const tgApp = isMiniApp() // spec 12 §12.4: no «Выйти» inside Telegram
  const badges = useBadges(token)
  const countOf = (i) => (i.badge ? badges[i.badge] || 0 : 0)
  const referralOn = useReferralOn()
  const items = ITEMS.filter((i) => (!i.company || company) && (!i.referral || referralOn))
  const isCurrent = (to) => {
    const [p, h] = to.split('#')
    if (h) return pathname === p && hash === `#${h}` ? 'page' : undefined
    return pathname === p && !(p === '/dashboard' && hash === '#referral') ? 'page' : undefined
  }

  useEffect(() => { setOpen(false) }, [pathname, hash])
  useLayer(drawer, open, () => { setOpen(false); btn.current?.focus() })
  useEffect(() => {
    if (!open) return undefined
    const mq = window.matchMedia('(min-width: 1025px)')
    const onWide = () => { if (mq.matches) setOpen(false) }
    mq.addEventListener('change', onWide)
    drawer.current?.querySelector('a,button')?.focus()
    return () => mq.removeEventListener('change', onWide)
  }, [open])

  const link = (i, tab = 0) => (
    <Link key={i.to} to={i.to} className="side-link" aria-current={isCurrent(i.to)} tabIndex={tab}
      aria-label={countOf(i) ? `${i.label}, новых: ${countOf(i)}` : undefined}>
      <Icon name={i.icon} />{i.label}
      {countOf(i) > 0 && <span className="side-link__count" aria-hidden="true">{countOf(i)}</span>}
    </Link>
  )

  return (
    <div className="app">
      <aside className="app__side" aria-label="Меню кабинета">
        <Logo style={{ padding: '6px 12px 18px' }} />
        <nav className="stack" style={{ gap: 2 }}>{items.map((i) => link(i))}</nav>
        <div className="stack gap-3" style={{ marginTop: 'auto', padding: 12 }}>
          {profile?.user_id && <IdChip id={profile.user_id} />}
          {!tgApp && <button type="button" className="t-sm muted" onClick={logoutDlg.show} style={{ background: 'none', border: 0, padding: 0, textAlign: 'left', cursor: 'pointer' }}>Выйти</button>}
        </div>
      </aside>

      <div className="app__main" style={{ paddingTop: 0 }}>
        {/* ≤1024: top bar */}
        <div className="lg:hidden row between gap-3" style={{ height: 'var(--header-h)', borderBottom: '1px solid var(--line)', margin: '0 -20px 20px', padding: '0 20px', position: 'sticky', top: 'var(--tg-top, 0px)', zIndex: 20, background: 'var(--bg)' }}>
          <Logo />
          <div className="row gap-2">
            {profile?.user_id && <IdChip id={profile.user_id} phone />}
            <button
              ref={btn}
              type="button"
              className={`menu-btn ${open ? 'is-open' : ''}`}
              aria-expanded={open}
              aria-controls="cabinet-drawer"
              aria-label={open ? 'Закрыть меню' : 'Меню'}
              onClick={() => setOpen((o) => !o)}
            >
              <span /><span />
            </button>
          </div>
        </div>
        <div className="max-lg:hidden" style={{ height: 28 }} />
        <main id="main">
          {profile?.must_change_password && <PasswordNotice here={pathname === '/settings'} />}
          {children}
        </main>
      </div>

      <div id="cabinet-drawer" ref={drawer} className={`drawer lg:hidden ${open ? 'is-open' : ''}`} aria-hidden={!open}>
        {items.map((i) => (
          <Link key={i.to} to={i.to} aria-current={isCurrent(i.to)} tabIndex={open ? 0 : -1}
            aria-label={countOf(i) ? `${i.label}, новых: ${countOf(i)}` : undefined}>
            {i.label}{countOf(i) > 0 && <span className="side-link__count" style={{ marginLeft: 8 }} aria-hidden="true">{countOf(i)}</span>}
          </Link>
        ))}
        <div className="drawer__foot">
          {profile?.user_id && <IdChip id={profile.user_id} />}
          {!tgApp && <Button variant="ghost" block onClick={() => { setOpen(false); logoutDlg.show() }} tabIndex={open ? 0 : -1}>Выйти</Button>}
        </div>
      </div>

      <LogoutDialog open={logoutDlg.open} onClose={logoutDlg.hide} profile={profile} />
    </div>
  )
}

// «Смените временный пароль» (02 §30): support reset the password, the person signed in with the
// temporary one. On every cabinet page, no close cross; it goes when the profile stops reporting
// must_change_password (the password was changed in Settings).
function PasswordNotice({ here }) {
  const navigate = useNavigate()
  const go = () => {
    if (!here) { navigate('/settings#password'); return }
    const sec = document.getElementById('password')
    sec?.scrollIntoView({ block: 'start' })
    sec?.querySelector('input[type="password"],input[autocomplete$="password"]')?.focus({ preventScroll: true })
  }
  return (
    <Alert tone="warn" title="Смените временный пароль." className="app__notice">
      Его выдала поддержка — придумайте свой, чтобы аккаунт был только вашим.
      <span className="app__notice-act"><Button variant="secondary" size="sm" onClick={go}>Сменить пароль</Button></span>
    </Alert>
  )
}

// Page head (02 §30): H1 + one muted line on the left, up to two page buttons on the right.
export function PageHead({ title, lead, actions }) {
  return (
    <div className="page-head">
      <div>
        <h1 className="t-h1">{title}</h1>
        {lead && <p className="muted" style={{ marginTop: 4 }}>{lead}</p>}
      </div>
      {actions && <div className="row gap-2">{actions}</div>}
    </div>
  )
}
