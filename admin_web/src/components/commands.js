import {
  LayoutDashboard, Users, Ticket, Wallet, Package, Server, Route, Send,
  Settings, ShieldCheck, FileText, Building2, TrendingUp, LogOut, Bell, Gift,
} from 'lucide-react'

/**
 * Command registry for the Cmd+K palette.
 *
 *   kind: 'page' — navigates via react-router navigate(href)
 *   kind: 'action' — runs perform(ctx) where ctx provides { logout, ... }
 */
export const commands = [
  // Overview
  { id: 'go-dashboard',     label: 'Дашборд',                icon: LayoutDashboard, kind: 'page', href: '/dashboard',     group: 'Обзор',          shortcut: ['⌘', 'D'] },

  // Users
  { id: 'go-users',         label: 'Пользователи',           icon: Users,           kind: 'page', href: '/users',         group: 'Пользователи',   shortcut: ['⌘', 'U'] },
  { id: 'go-promocodes',    label: 'Промокоды',              icon: Ticket,          kind: 'page', href: '/promocodes',    group: 'Пользователи' },

  // Infrastructure
  { id: 'go-servers',         label: 'Серверы',              icon: Server,          kind: 'page', href: '/servers',         group: 'Инфраструктура' },
  { id: 'go-direct-routes',   label: 'Прямая маршрутизация', icon: Route,           kind: 'page', href: '/direct-routes',   group: 'Инфраструктура' },
  { id: 'go-mtproto',         label: 'Telegram',             icon: Send,            kind: 'page', href: '/mtproto',         group: 'Инфраструктура' },
  { id: 'go-alerts',          label: 'Алерты',               icon: Bell,            kind: 'page', href: '/alerts',          group: 'Инфраструктура' },

  // Money
  { id: 'go-plans',        label: 'Тарифы',                  icon: Package,         kind: 'page', href: '/plans',        group: 'Деньги' },
  { id: 'go-payments',     label: 'Платежи',                 icon: Wallet,          kind: 'page', href: '/payments',     group: 'Деньги' },
  { id: 'go-referrals',    label: 'Приведи друга',           icon: Gift,            kind: 'page', href: '/referrals',    group: 'Деньги' },

  // Admin
  { id: 'go-admins',         label: 'Администраторы',        icon: ShieldCheck,     kind: 'page', href: '/admins',        group: 'Админ' },
  { id: 'go-audit-log',      label: 'Журнал аудита',         icon: FileText,        kind: 'page', href: '/audit-log',     group: 'Админ' },
  { id: 'go-tenants',        label: 'Тенанты',               icon: Building2,       kind: 'page', href: '/tenants',       group: 'Админ' },
  { id: 'go-b2b-analytics',  label: 'B2B Аналитика',         icon: TrendingUp,      kind: 'page', href: '/b2b-analytics', group: 'Админ' },
  { id: 'go-settings',       label: 'Настройки',             icon: Settings,        kind: 'page', href: '/settings',      group: 'Админ' },

  // Actions
  { id: 'action-logout', label: 'Выйти из админки', icon: LogOut, kind: 'action', perform: 'logout', group: 'Действия', shortcut: ['⇧', '⌘', 'Q'] },
]
