import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard, Users, UsersRound, CreditCard, Ticket, Wallet, RotateCcw,
  Package, BarChart3, Server, Key, Rocket, EyeOff, Route, Send, Zap,
  Settings, ShieldCheck, FileText, Building2, TrendingUp, LogOut, Search,
} from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useT } from '../i18n/index.jsx'
import { Kbd } from './ui/index.js'

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
      { to: '/groups',        icon: UsersRound, label: 'navGroups',        perm: 'groups.read' },
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
      { to: '/plans',        icon: Package,  label: 'navPlans',       perm: 'settings.read' },
      { to: '/payments',     icon: Wallet,   label: 'navPayments',    perm: 'payments.read' },
      { to: '/payment-ops',  icon: RotateCcw, label: 'navPaymentOps', perm: 'payments.read' },
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

export default function Sidebar({ onCmdK }) {
  const { can, isTenantAdmin, admin, logout } = useAuth()
  const { t } = useT()

  const renderItem = (item) => {
    if (item.hideForTenant && isTenantAdmin) return null
    if (item.perm && !can(item.perm)) return null
    const Icon = item.icon
    return (
      <NavLink
        key={item.to}
        to={item.to}
        className={({ isActive }) =>
          `flex items-center gap-2 h-8 px-3 rounded text-sm transition-colors ${
            isActive
              ? 'bg-accent/10 text-primary border-l-2 border-accent pl-[10px]'
              : 'text-secondary hover:bg-hover hover:text-primary'
          }`
        }
      >
        <Icon className="w-4 h-4 shrink-0" />
        <span className="flex-1 truncate">{t[item.label] || item.label}</span>
      </NavLink>
    )
  }

  return (
    <aside className="w-60 shrink-0 h-screen sticky top-0 bg-elevated border-r border-default flex flex-col">
      {/* Header */}
      <div className="h-12 px-4 flex items-center border-b border-default">
        <div className="font-semibold text-primary tracking-tight">HiEarth Admin</div>
      </div>

      {/* Cmd+K trigger */}
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

      {/* Groups */}
      <nav className="flex-1 overflow-y-auto px-2 pb-3 space-y-0.5">
        {groups.map((g) => {
          const visible = g.items
            .map(renderItem)
            .filter(Boolean)
          if (visible.length === 0) return null
          return (
            <div key={g.label}>
              <div className="text-[10px] uppercase tracking-wider text-tertiary px-3 mt-4 mb-1">
                {g.label}
              </div>
              <div className="space-y-0.5">{visible}</div>
            </div>
          )
        })}
      </nav>

      {/* User dropdown */}
      <div className="border-t border-default p-3">
        <div className="flex items-center gap-2">
          <div className="w-7 h-7 rounded-full bg-accent/20 text-accent flex items-center justify-center text-xs font-semibold">
            {(admin?.username || 'A').charAt(0).toUpperCase()}
          </div>
          <div className="flex-1 min-w-0">
            <div className="text-sm text-primary truncate">{admin?.username || 'admin'}</div>
            <div className="text-[11px] text-tertiary truncate">{(admin?.roles || []).join(', ')}</div>
          </div>
          <button
            onClick={logout}
            aria-label="Выйти"
            className="p-1.5 rounded text-tertiary hover:text-danger hover:bg-hover transition-colors"
          >
            <LogOut className="w-4 h-4" />
          </button>
        </div>
      </div>
    </aside>
  )
}
