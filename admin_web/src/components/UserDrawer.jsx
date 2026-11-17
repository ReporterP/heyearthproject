import { useState, useEffect } from 'react'
import { Trash2, Ban, CircleCheck, KeyRound } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from './Toast.jsx'
import { updateUser, deleteUser } from '../api/admin.js'
import { Drawer } from './ui/Drawer.jsx'
import { Button, Badge, Dialog, ConfirmDanger } from './ui/index.js'
import { hasPlan, passwordState } from '../utils/users.js'
import ProfileTab from './user-drawer/ProfileTab.jsx'
import SubscriptionsTab from './user-drawer/SubscriptionsTab.jsx'
import TrafficPaymentsTab from './user-drawer/TrafficPaymentsTab.jsx'
import WalletTab from './user-drawer/WalletTab.jsx'
import PasswordResetDialog from './user-drawer/PasswordResetDialog.jsx'

const TABS = [
  { id: 'profile',       key: 'drawerTabProfile' },
  { id: 'subscriptions', key: 'drawerTabSubs' },
  { id: 'traffic',       key: 'drawerTabTraffic' },
  { id: 'wallet',        key: 'drawerTabWallet' },
]

export default function UserDrawer({ user, defaultTab = 'profile', onClose, onUserMutated }) {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [activeTab, setActiveTab] = useState(defaultTab)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [confirmBlock, setConfirmBlock] = useState(false)
  const [resetOpen, setResetOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  // Sync activeTab when defaultTab changes (e.g. URL state ?tab=subscriptions)
  useEffect(() => { setActiveTab(defaultTab) }, [defaultTab, user?.id])

  if (!user) return null

  async function toggleBlock() {
    setBusy(true)
    try {
      await updateUser(user.id, { is_blocked: !user.is_blocked })
      toast.success(user.is_blocked ? 'Пользователь разблокирован' : 'Пользователь заблокирован — подключения закроются в течение минуты')
      setConfirmBlock(false)
      onUserMutated?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  // Behind ConfirmDanger: a refusal (the user paid — the API keeps money on record) stays in the dialog.
  async function handleDelete() {
    await deleteUser(user.id)
    toast.success('Пользователь удалён')
    onUserMutated?.()
    onClose?.()
  }

  const headerActions = (
    <div className="flex items-center gap-2 flex-wrap">
      {can('users.write') && (user.is_blocked ? (
        <Button size="sm" variant="secondary" loading={busy} onClick={toggleBlock} icon={<CircleCheck className="w-3.5 h-3.5" />}>Разблокировать</Button>
      ) : (
        <Button size="sm" variant="secondary" className="text-danger" onClick={() => setConfirmBlock(true)} icon={<Ban className="w-3.5 h-3.5" />}>Заблокировать</Button>
      ))}
      {can('users.password_reset') && passwordState(user).canReset && (
        <Button size="sm" variant="secondary" onClick={() => setResetOpen(true)} icon={<KeyRound className="w-3.5 h-3.5" />}>Сбросить пароль</Button>
      )}
      {can('users.write') && (
        <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirmDelete(true)} icon={<Trash2 className="w-3.5 h-3.5" />}>
          Удалить
        </Button>
      )}
    </div>
  )

  const subtitle = (
    <span className="flex items-center gap-2 text-xs">
      <span className="font-mono text-tertiary">{user.user_id}</span>
      <span className="text-tertiary">·</span>
      <Badge variant={user.is_blocked ? 'danger' : (hasPlan(user) ? 'accent' : 'default')}>
        {user.is_blocked ? 'заблокирован' : (hasPlan(user) ? user.current_plan : 'без подписки')}
      </Badge>
    </span>
  )

  return (
    <>
      <Drawer
        open={!!user}
        onClose={onClose}
        title={user.username || user.user_id}
        subtitle={subtitle}
        actions={headerActions}
      >
        {/* Tabs strip */}
        <nav className="flex items-center gap-1 border-b border-default px-4 sticky top-0 bg-canvas z-10">
          {TABS.map(tab => (
            <button
              key={tab.id}
              data-tab={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={`px-3 py-2 text-sm font-medium rounded-t transition relative ${
                activeTab === tab.id
                  ? 'text-accent bg-accent/10'
                  : 'text-secondary hover:text-primary hover:bg-hover'
              }`}
            >
              {t[tab.key]}
              {activeTab === tab.id && (
                <span className="absolute left-0 right-0 -bottom-px h-0.5 bg-accent" />
              )}
            </button>
          ))}
        </nav>

        {/* Active tab body */}
        <div className="p-4 space-y-4">
          {activeTab === 'profile'       && <ProfileTab user={user} onSaved={onUserMutated} />}
          {activeTab === 'subscriptions' && <SubscriptionsTab user={user} onChanged={onUserMutated} />}
          {activeTab === 'traffic'       && <TrafficPaymentsTab user={user} />}
          {activeTab === 'wallet'        && <WalletTab user={user} />}
        </div>
      </Drawer>

      <Dialog open={confirmBlock} onClose={() => setConfirmBlock(false)} title="Заблокировать пользователя" maxWidth="max-w-md">
        <div className="space-y-3 text-sm">
          <ul className="list-disc pl-5 space-y-1 text-secondary">
            <li>Все подключения пользователя перестанут работать в течение минуты, новые не пройдут.</li>
            <li>Подписка, кошелёк и история платежей сохраняются; срок подписки идёт дальше.</li>
            <li>Разблокировать можно в любой момент этой же кнопкой.</li>
          </ul>
          <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2">
            <Button variant="ghost" onClick={() => setConfirmBlock(false)}>Отмена</Button>
            <Button variant="danger" loading={busy} onClick={toggleBlock}>Заблокировать</Button>
          </div>
        </div>
      </Dialog>

      <PasswordResetDialog user={user} open={resetOpen} onClose={() => setResetOpen(false)} onDone={onUserMutated} />

      <ConfirmDanger open={confirmDelete} onClose={() => setConfirmDelete(false)} title="Удалить пользователя"
        confirmText={user.user_id} actionLabel="Удалить"
        consequences={[
          'Запись пользователя, его подписки, устройства и сессии удаляются безвозвратно.',
          'Если пользователь хоть раз платил или пополнял кошелёк, удалить нельзя (учёт и чеки) — заблокируйте его.',
        ]}
        onConfirm={handleDelete} />
    </>
  )
}
