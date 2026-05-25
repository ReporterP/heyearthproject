import { useState } from 'react'
import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard, Users, CreditCard, Ticket, Wallet, RotateCcw,
  Package, BarChart3, Server, Key, Rocket, EyeOff, Route, Send, Zap,
  Settings, ShieldCheck, FileText, Building2, TrendingUp, LogOut, Search,
  ChevronDown, PanelLeftClose, PanelLeftOpen, Palette, X,
} from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useT } from '../i18n/index.jsx'
import { Kbd } from './ui/index.js'
import ThemePicker from './ThemePicker.jsx'

const groups = [
  {
    label: 'ОБЗОР',
    items: [
      { to: '/dashboard', icon: LayoutDashboard, label: 'navDashboard',  perm: 'dashboard.read' },
      { to: '/traffic',   icon: BarChart3,        label: 'navTraffic',    perm: 'dashboard.read' },
    ],
  },
  {
    label: 'ПОЛЬЗОВАТЕЛИ',
    items: [
      { to: '/users',         icon: Users,      label: 'navUsers',         perm: 'users.read' },
      { to: '/subscriptions', icon: CreditCard, label: 'navSubscriptions', perm: 'subscriptions.read' },
      { to: '/promocodes',    icon: Ticket,     label: 'navPromocodes',    perm: 'promocodes.read' },
    ],
  },
  {
    label: 'ИНФРАСТРУКТУРА',
    items: [
      { to: '/servers',          icon: Server,   label: 'navServers',        perm: 'servers.read',           hideForTenant: true },
      { to: '/ssh-keys',         icon: Key,      label: 'navSshKeys',        perm: 'servers.read',           hideForTenant: true },
      { to: '/releases',         icon: Rocket,   label: 'navReleases',       perm: 'releases.read',          hideForTenant: true },
      { to: '/traffic-masking',  icon: EyeOff,   label: 'navTrafficMasking', perm: 'traffic_masking.read',   hideForTenant: true },
      { to: '/direct-routes',    icon: Route,    label: 'navDirectRoutes',   perm: 'routing.direct.view',    hideForTenant: true },
      { to: '/mtproto',          icon: Send,     label: 'navMTProto',        perm: 'settings.read',          hideForTenant: true },
      { to: '/auto-tuner',       icon: Zap,      label: 'navAutoTuner',      perm: 'settings.read',          hideForTenant: true },
    ],
  },
  {
    label: 'ДЕНЬГИ',
    items: [
      { to: '/plans',        icon: Package,   label: 'navPlans',       perm: 'settings.read' },
      { to: '/payments',     icon: Wallet,    label: 'navPayments',    perm: 'payments.read' },
      { to: '/payment-ops',  icon: RotateCcw, label: 'navPaymentOps',  perm: 'payments.read' },
    ],
  },
  {
    label: 'АДМИН',
    items: [
      { to: '/admins',        icon: ShieldCheck, label: 'navAdmins',       perm: 'admins.read',  hideForTenant: true },
      { to: '/audit-log',     icon: FileText,    label: 'navAuditLog',     perm: 'audit.read',   hideForTenant: true },
      { to: '/tenants',       icon: Building2,   label: 'navTenants',      perm: 'tenants.read', hideForTenant: true },
      { to: '/b2b-analytics', icon: TrendingUp,  label: 'navB2BAnalytics', perm: 'tenants.read', hideForTenant: true },
      { to: '/settings',      icon: Settings,    label: 'navSettings',     perm: null,           hideForTenant: true },
    ],
  },
]

const GROUPS_KEY = 'hiearth-sidebar-groups'

function loadOpenGroups() {
  try { return JSON.parse(localStorage.getItem(GROUPS_KEY) || '{}') } catch { return {} }
}

export default function Sidebar({ collapsed, onToggleCollapse, onCmdK, mobile = false, onMobileNav }) {
  const { can, isTenantAdmin, admin, logout } = useAuth()
  const { t } = useT()
  const [openGroups, setOpenGroups] = useState(loadOpenGroups)
  const [pickerOpen, setPickerOpen] = useState(false)

  const isGroupOpen = (label) => openGroups[label] !== false
  const toggleGroup = (label) => {
    const next = { ...openGroups, [label]: isGroupOpen(label) ? false : true }
    setOpenGroups(next)
    localStorage.setItem(GROUPS_KEY, JSON.stringify(next))
  }

  const filterItems = (items) =>
    items.filter((i) => !(i.hideForTenant && isTenantAdmin) && !(i.perm && !can(i.perm)))

  return (
    <aside
      className={`shrink-0 h-screen bg-elevated border-r border-default flex flex-col ${
        mobile ? '' : 'sticky top-0'
      }`}
      style={mobile
        ? { width: '280px' }
        : { width: collapsed ? '60px' : '240px', transition: 'width 150ms ease' }}
    >
      {/* Header */}
      <div className="h-12 px-3 flex items-center justify-between border-b border-default">
        {!collapsed && <div className="font-semibold text-primary tracking-tight truncate">HiEarth Admin</div>}
        <button
          onClick={onToggleCollapse}
          aria-label={mobile ? 'Закрыть меню' : (collapsed ? 'Развернуть боковую панель' : 'Свернуть боковую панель')}
          className={`p-1.5 rounded text-tertiary hover:text-primary hover:bg-hover ${(!mobile && collapsed) ? 'mx-auto' : ''}`}
        >
          {mobile
            ? <X className="w-4 h-4" />
            : (collapsed ? <PanelLeftOpen className="w-4 h-4" /> : <PanelLeftClose className="w-4 h-4" />)}
        </button>
      </div>

      {/* Cmd+K trigger */}
      {!collapsed ? (
        <button
          type="button"
          onClick={onCmdK}
          className="mx-2 mt-3 mb-1 flex items-center gap-2 h-8 px-2.5 rounded border border-default bg-canvas hover:bg-hover transition-colors text-sm text-tertiary hover:text-primary"
        >
          <Search className="w-3.5 h-3.5" />
          <span className="flex-1 text-left">Поиск...</span>
          <span className="flex gap-0.5">
            <Kbd>⌘</Kbd>
            <Kbd>K</Kbd>
          </span>
        </button>
      ) : (
        <button
          type="button"
          onClick={onCmdK}
          aria-label="Поиск (⌘K)"
          className="mx-auto mt-3 mb-1 w-9 h-9 flex items-center justify-center rounded text-tertiary hover:text-primary hover:bg-hover"
        >
          <Search className="w-4 h-4" />
        </button>
      )}

      {/* Groups */}
      <nav className="flex-1 overflow-y-auto px-2 pb-3 space-y-0.5">
        {groups.map((g) => {
          const visible = filterItems(g.items)
          if (visible.length === 0) return null

          if (collapsed) {
            return (
              <div key={g.label} className="mt-2 first:mt-0">
                {visible.map((item) => <SidebarItem key={item.to} item={item} t={t} collapsed onMobileNav={onMobileNav} />)}
                <div className="my-2 mx-2 border-t border-default/40" />
              </div>
            )
          }

          const open = isGroupOpen(g.label)
          return (
            <div key={g.label}>
              <button
                type="button"
                onClick={() => toggleGroup(g.label)}
                className="w-full flex items-center justify-between px-3 mt-4 mb-1 text-[10px] uppercase tracking-wider text-tertiary hover:text-secondary"
              >
                <span>{g.label}</span>
                <ChevronDown className={`w-3 h-3 transition-transform ${open ? '' : '-rotate-90'}`} />
              </button>
              {open && (
                <div className="space-y-0.5">
                  {visible.map((item) => <SidebarItem key={item.to} item={item} t={t} collapsed={false} onMobileNav={onMobileNav} />)}
                </div>
              )}
            </div>
          )
        })}
      </nav>

      {/* Footer */}
      <div className="border-t border-default p-2 relative">
        {collapsed ? (
          <div className="flex flex-col gap-1">
            <button
              onClick={() => setPickerOpen(!pickerOpen)}
              aria-label="Тема и акцент"
              className="w-9 h-9 mx-auto rounded text-tertiary hover:text-primary hover:bg-hover flex items-center justify-center"
            >
              <Palette className="w-4 h-4" />
            </button>
            <button
              onClick={logout}
              aria-label="Выйти"
              className="w-9 h-9 mx-auto rounded text-tertiary hover:text-danger hover:bg-hover flex items-center justify-center"
            >
              <LogOut className="w-4 h-4" />
            </button>
          </div>
        ) : (
          <div className="flex items-center gap-2 px-1">
            <div className="w-7 h-7 rounded-full bg-accent/20 text-accent flex items-center justify-center text-xs font-semibold shrink-0">
              {(admin?.username || 'A').charAt(0).toUpperCase()}
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-sm text-primary truncate">{admin?.username || 'admin'}</div>
              <div className="text-[11px] text-tertiary truncate">{(admin?.roles || []).join(', ')}</div>
            </div>
            <button
              onClick={() => setPickerOpen(!pickerOpen)}
              aria-label="Тема и акцент"
              className="p-1.5 rounded text-tertiary hover:text-primary hover:bg-hover"
            >
              <Palette className="w-4 h-4" />
            </button>
            <button
              onClick={logout}
              aria-label="Выйти"
              className="p-1.5 rounded text-tertiary hover:text-danger hover:bg-hover"
            >
              <LogOut className="w-4 h-4" />
            </button>
          </div>
        )}
        <ThemePicker
          open={pickerOpen}
          onClose={() => setPickerOpen(false)}
          anchor={collapsed ? 'left-collapsed' : 'footer'}
        />
      </div>
    </aside>
  )
}

function SidebarItem({ item, t, collapsed, onMobileNav }) {
  const Icon = item.icon
  const label = t[item.label] || item.label
  return (
    <NavLink
      to={item.to}
      title={collapsed ? label : undefined}
      aria-label={collapsed ? label : undefined}
      onClick={() => { onMobileNav && onMobileNav() }}
      className={({ isActive }) =>
        `flex items-center gap-2 h-8 rounded text-sm transition-colors ${
          collapsed ? 'justify-center w-9 mx-auto px-0' : 'px-3'
        } ${
          isActive
            ? 'bg-accent/10 text-primary border-l-2 border-accent ' + (collapsed ? '' : 'pl-[10px]')
            : 'text-secondary hover:bg-hover hover:text-primary'
        }`
      }
    >
      <Icon className="w-4 h-4 shrink-0" />
      {!collapsed && <span className="flex-1 truncate">{label}</span>}
    </NavLink>
  )
}
