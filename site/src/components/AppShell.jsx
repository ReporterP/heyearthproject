import { useEffect, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount from '../account/useAccount.js'
import useSiteInfo from '../hooks/useSiteInfo.js'
import { apiFetch } from '../api/http.js'
import Logo from './Logo.jsx'
import Icon from './Icon.jsx'
import Button from './Button.jsx'
import Dialog, { DialogActions } from './Dialog.jsx'
import { IdChip, shortId } from './Nomerok.jsx'

// AppShell (spec 02 §30): the cabinet's side menu + main column. ≤1024 px the menu becomes a top bar
// (logo · short ID chip · menu button) with the items in a drawer. «Выйти» opens the logout dialog.
let companyCache = null // null = unknown, false = no company, true = member with a dashboard

function useCompany(token, enabled) {
  const [has, setHas] = useState(companyCache === true)
  useEffect(() => {
    if (!enabled || !token || companyCache !== null) return undefined
    let alive = true
    apiFetch('/api/v1/business/me', { token })
      .then(() => { companyCache = true; if (alive) setHas(true) })
      .catch(() => { companyCache = false })
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

const ITEMS = [
  { to: '/dashboard', label: 'Обзор', icon: 'home' },
  { to: '/connect', label: 'Подключение', icon: 'power' },
  { to: '/wallet', label: 'Кошелёк', icon: 'wallet' },
  { to: '/payments', label: 'Оплата и подписка', icon: 'receipt' },
  { to: '/dashboard#referral', label: 'Пригласить друга', icon: 'gift' },
  { to: '/business/dashboard', label: 'Компания', icon: 'building', company: true },
  { to: '/settings', label: 'Настройки', icon: 'settings' },
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

  const items = ITEMS.filter((i) => !i.company || company)
  const isCurrent = (to) => {
    const [p, h] = to.split('#')
    if (h) return pathname === p && hash === `#${h}` ? 'page' : undefined
    return pathname === p && !(p === '/dashboard' && hash === '#referral') ? 'page' : undefined
  }

  useEffect(() => { setOpen(false) }, [pathname, hash])
  useEffect(() => {
    if (!open) return undefined
    const onKey = (e) => { if (e.key === 'Escape') { setOpen(false); btn.current?.focus() } }
    const mq = window.matchMedia('(min-width: 1025px)')
    const onWide = () => { if (mq.matches) setOpen(false) }
    window.addEventListener('keydown', onKey)
    mq.addEventListener('change', onWide)
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    drawer.current?.querySelector('a,button')?.focus()
    return () => { window.removeEventListener('keydown', onKey); mq.removeEventListener('change', onWide); document.body.style.overflow = prev }
  }, [open])

  const link = (i, tab = 0) => (
    <Link key={i.to} to={i.to} className="side-link" aria-current={isCurrent(i.to)} tabIndex={tab}>
      <Icon name={i.icon} />{i.label}
    </Link>
  )

  return (
    <div className="app">
      <aside className="app__side" aria-label="Меню кабинета">
        <Logo style={{ padding: '6px 12px 18px' }} />
        <nav className="stack" style={{ gap: 2 }}>{items.map((i) => link(i))}</nav>
        <div className="stack gap-3" style={{ marginTop: 'auto', padding: 12 }}>
          {profile?.user_id && <IdChip id={profile.user_id} />}
          <button type="button" className="t-sm muted" onClick={logoutDlg.show} style={{ background: 'none', border: 0, padding: 0, textAlign: 'left', cursor: 'pointer' }}>Выйти</button>
        </div>
      </aside>

      <div className="app__main" style={{ paddingTop: 0 }}>
        {/* ≤1024: top bar */}
        <div className="lg:hidden row between gap-3" style={{ height: 'var(--header-h)', borderBottom: '1px solid var(--line)', margin: '0 -20px 20px', padding: '0 20px', position: 'sticky', top: 0, zIndex: 20, background: 'var(--bg)' }}>
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
        <main id="main">{children}</main>
      </div>

      <div id="cabinet-drawer" ref={drawer} className={`drawer lg:hidden ${open ? 'is-open' : ''}`} aria-hidden={!open}>
        {items.map((i) => (
          <Link key={i.to} to={i.to} aria-current={isCurrent(i.to)} tabIndex={open ? 0 : -1}>{i.label}</Link>
        ))}
        <div className="drawer__foot">
          {profile?.user_id && <IdChip id={profile.user_id} />}
          <Button variant="ghost" block onClick={() => { setOpen(false); logoutDlg.show() }} tabIndex={open ? 0 : -1}>Выйти</Button>
        </div>
      </div>

      <LogoutDialog open={logoutDlg.open} onClose={logoutDlg.hide} profile={profile} />
    </div>
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
