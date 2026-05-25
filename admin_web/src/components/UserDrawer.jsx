import { useState, useEffect } from 'react'
import { Trash2, Smartphone } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from './Toast.jsx'
import { updateUser, deleteUser, fetchUserDevices } from '../api/admin.js'
import { Drawer } from './ui/Drawer.jsx'
import { Button, Badge, Dialog } from './ui/index.js'
import ConfirmDialog from './ConfirmDialog.jsx'
import ProfileTab from './user-drawer/ProfileTab.jsx'
import SubscriptionsTab from './user-drawer/SubscriptionsTab.jsx'
import TrafficPaymentsTab from './user-drawer/TrafficPaymentsTab.jsx'

const TABS = [
  { id: 'profile',       key: 'drawerTabProfile' },
  { id: 'subscriptions', key: 'drawerTabSubs' },
  { id: 'traffic',       key: 'drawerTabTraffic' },
]

export default function UserDrawer({ user, defaultTab = 'profile', onClose, onUserMutated }) {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [activeTab, setActiveTab] = useState(defaultTab)
  const [devicesOpen, setDevicesOpen] = useState(false)
  const [devicesItems, setDevicesItems] = useState([])
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [busy, setBusy] = useState(false)

  // Sync activeTab when defaultTab changes (e.g. URL state ?tab=subscriptions)
  useEffect(() => { setActiveTab(defaultTab) }, [defaultTab, user?.id])

  if (!user) return null

  async function openDevices() {
    try {
      const devs = await fetchUserDevices(user.id)
      const items = Array.isArray(devs) ? devs : (Array.isArray(devs?.devices) ? devs.devices : [])
      setDevicesItems(items)
      setDevicesOpen(true)
    } catch (err) {
      toast.error(err.message)
    }
  }

  async function toggleBlock() {
    setBusy(true)
    try {
      await updateUser(user.id, { is_blocked: !user.is_blocked })
      toast.success(!user.is_blocked ? (t.quickBlock + ' OK') : (t.quickUnblock + ' OK'))
      onUserMutated?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete() {
    setBusy(true)
    try {
      await deleteUser(user.id)
      toast.success(t.delete + ' OK')
      setConfirmDelete(false)
      onUserMutated?.()
      onClose?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  const headerActions = (
    <div className="flex items-center gap-2 flex-wrap">
      <Button size="sm" variant="ghost" onClick={openDevices} icon={<Smartphone className="w-3.5 h-3.5" />}>
        Devices
      </Button>
      {can('users.write') && (
        <button
          onClick={toggleBlock}
          disabled={busy}
          title={user.is_blocked ? t.quickUnblock : t.quickBlock}
          aria-label={user.is_blocked ? t.quickUnblock : t.quickBlock}
          className={`relative inline-flex h-5 w-9 items-center rounded-full transition ${user.is_blocked ? 'bg-danger/30' : 'bg-success/30'}`}
        >
          <span className={`inline-block h-3.5 w-3.5 rounded-full transition-transform ${user.is_blocked ? 'translate-x-1 bg-danger' : 'translate-x-[18px] bg-success'}`} />
        </button>
      )}
      {can('users.write') && (
        <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirmDelete(true)} icon={<Trash2 className="w-3.5 h-3.5" />}>
          {t.delete}
        </Button>
      )}
    </div>
  )

  const subtitle = (
    <span className="flex items-center gap-2 text-xs">
      <span className="font-mono text-tertiary">{user.user_id}</span>
      <span className="text-tertiary">·</span>
      <Badge variant={user.is_blocked ? 'danger' : (user.plan ? 'accent' : 'default')}>
        {user.is_blocked ? 'blocked' : (user.plan || 'no plan')}
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
        </div>
      </Drawer>

      {/* Devices modal — reuses existing Dialog primitive, not nested in Drawer */}
      <Dialog
        open={devicesOpen}
        onClose={() => setDevicesOpen(false)}
        title={t.registeredDevicesLabel(devicesItems.length)}
        maxWidth="max-w-md"
      >
        {devicesItems.length === 0 ? (
          <p className="text-secondary text-sm">{t.usersNoDevices || 'No devices'}</p>
        ) : (
          <div className="space-y-2 max-h-60 overflow-y-auto">
            {devicesItems.map((d, i) => (
              <div key={i} className="text-sm bg-elevated rounded p-3 border border-default">
                <p className="font-mono text-xs text-tertiary">{d.device_id}</p>
                <p className="text-primary">{d.platform} {d.app_version}</p>
                {d.last_seen && <p className="text-xs text-tertiary">{t.deviceLastSeen}: {new Date(d.last_seen).toLocaleString()}</p>}
              </div>
            ))}
          </div>
        )}
        <div className="flex justify-end mt-4">
          <Button variant="ghost" onClick={() => setDevicesOpen(false)}>{t.close}</Button>
        </div>
      </Dialog>

      <ConfirmDialog
        open={confirmDelete}
        message={t.deleteUserConfirm(user.user_id || user.id)}
        onConfirm={handleDelete}
        onCancel={() => setConfirmDelete(false)}
        loading={busy}
      />
    </>
  )
}
