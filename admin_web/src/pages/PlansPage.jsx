import { useState, useEffect } from 'react'
import { Package } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchPlans, createPlan, updatePlan, deletePlan, fetchServersPaged } from '../api/admin.js'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import { formatBytes } from '../utils/format.js'
import {
  Badge,
  Button,
  Card,
  Dialog,
  EmptyState,
  Input,
  PageHeader,
  Spinner,
  Textarea,
} from '../components/ui/index.js'

const PROTOCOL_OPTIONS = ['all', 'vless', 'socks', 'http']

export default function PlansPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [plans, setPlans] = useState([])
  const [servers, setServers] = useState([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState(null) // null | plan object
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    Promise.all([
      fetchPlans().catch(() => []),
      fetchServersPaged({ page_size: 100 }).catch(() => ({ items: [] })),
    ]).then(([p, s]) => {
      setPlans(Array.isArray(p) ? p : [])
      setServers(s?.items || [])
    }).finally(() => setLoading(false))
  }, [])

  async function reload() {
    const p = await fetchPlans().catch(() => [])
    setPlans(Array.isArray(p) ? p : [])
  }

  function openCreate() {
    setEditing({
      slug: '', name: '', description: '', price_rub: 0, duration_days: 30,
      traffic_limit_bytes: null, allowed_protocols: ['all'], features: '{}',
      is_active: true, direct_routing_enabled: false, sort_order: 0, auto_renew_default: true, server_ids: [],
    })
  }

  function openEdit(plan) {
    setEditing({ ...plan })
  }

  async function handleSave() {
    const p = { ...editing }
    if (!p.slug || !p.name) {
      toast.error('slug и name обязательны')
      return
    }
    setSaving(true)
    try {
      if (p.id) {
        await updatePlan(p.id, p)
        toast.success('План обновлён')
      } else {
        await createPlan(p)
        toast.success('План создан')
      }
      setEditing(null)
      reload()
    } catch (e) {
      toast.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(plan) {
    try {
      await deletePlan(plan.id)
      toast.success('План удалён')
      reload()
    } catch (e) {
      toast.error(e.message)
    }
    setConfirm(null)
  }

  if (loading) return (
    <div className="flex justify-center py-20">
      <Spinner size="md" />
    </div>
  )

  return (
    <div className="space-y-4">
      <PageHeader
        title="Тарифные планы"
        subtitle="Конструктор подписок: протоколы, серверы, трафик, цена"
        actions={can('settings.write') ? (
          <Button variant="primary" onClick={openCreate}>+ Создать план</Button>
        ) : null}
      />

      {plans.length === 0 ? (
        <EmptyState
          icon={<Package className="w-12 h-12" />}
          title="Нет тарифных планов"
          description="Создайте первый план: задайте цену, срок, разрешённые протоколы и серверы."
          action={can('settings.write') ? (
            <Button variant="primary" onClick={openCreate}>+ Создать план</Button>
          ) : null}
        />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {plans.map(plan => (
            <Card key={plan.id} className={`p-5 transition-colors ${plan.is_active ? '' : 'border-danger/30 opacity-60'}`}>
              <div className="flex items-start justify-between mb-3">
                <div>
                  <h3 className={`font-semibold text-lg ${plan.is_active ? 'text-primary' : 'line-through text-tertiary'}`}>{plan.name}</h3>
                  <p className="text-xs text-tertiary font-mono">{plan.slug}</p>
                </div>
                <div className="text-right">
                  <p className="text-2xl font-semibold tabular-nums font-mono text-accent">{plan.price_rub}₽</p>
                  <p className="text-xs text-tertiary">{plan.duration_days} дн.</p>
                </div>
              </div>

              {plan.description && <p className="text-sm text-secondary mb-3">{plan.description}</p>}

              <div className="flex flex-wrap gap-1.5 mb-3">
                {(plan.allowed_protocols || []).map(p => (
                  <Badge key={p} variant="accent" className="uppercase">{p}</Badge>
                ))}
              </div>

              <div className="text-xs text-tertiary space-y-1 mb-3">
                <p>Трафик: <span className="text-primary font-medium">{plan.traffic_limit_bytes ? formatBytes(plan.traffic_limit_bytes) : 'Безлимит'}</span></p>
                <p>Серверов: <span className="text-primary font-medium">{plan.server_ids?.length || 0}</span></p>
                <p>Автопродление: <span className="text-primary">{plan.auto_renew_default ? 'Да' : 'Нет'}</span></p>
                <p>Доступ к РФ-сервисам: <span className="text-primary">{plan.direct_routing_enabled ? 'Включён' : 'Выключен'}</span></p>
                {!plan.is_active && <p className="text-danger font-medium">Неактивен</p>}
              </div>

              {can('settings.write') && (
                <div className="flex gap-2 pt-3 border-t border-default/60">
                  <Button size="sm" variant="secondary" onClick={() => openEdit(plan)}>Редактировать</Button>
                  <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirm(plan)}>Удалить</Button>
                </div>
              )}
            </Card>
          ))}
        </div>
      )}

      {/* Edit/Create modal */}
      <Dialog
        open={!!editing}
        onClose={() => setEditing(null)}
        title={editing?.id ? 'Редактировать план' : 'Создать план'}
        maxWidth="max-w-2xl"
      >
        {editing && (
          <>
            <div className="grid grid-cols-2 gap-4 mb-4">
              <div>
                <label className="text-xs text-tertiary block mb-1">Slug (URL-ключ)</label>
                <Input
                  value={editing.slug}
                  onChange={e => setEditing({...editing, slug: e.target.value})}
                  placeholder="vless-reality"
                  className="font-mono"
                />
              </div>
              <div>
                <label className="text-xs text-tertiary block mb-1">Название</label>
                <Input
                  value={editing.name}
                  onChange={e => setEditing({...editing, name: e.target.value})}
                  placeholder="SOCKS Basic"
                />
              </div>
            </div>

            <div className="mb-4">
              <label className="text-xs text-tertiary block mb-1">Описание</label>
              <Textarea
                value={editing.description || ''}
                onChange={e => setEditing({...editing, description: e.target.value})}
                rows={2}
              />
            </div>

            <div className="grid grid-cols-3 gap-4 mb-4">
              <div>
                <label className="text-xs text-tertiary block mb-1">Цена (₽)</label>
                <Input
                  type="number"
                  value={editing.price_rub}
                  onChange={e => setEditing({...editing, price_rub: parseInt(e.target.value) || 0})}
                />
              </div>
              <div>
                <label className="text-xs text-tertiary block mb-1">Срок (дни)</label>
                <Input
                  type="number"
                  value={editing.duration_days}
                  onChange={e => setEditing({...editing, duration_days: parseInt(e.target.value) || 30})}
                />
              </div>
              <div>
                <label className="text-xs text-tertiary block mb-1">Трафик (ГБ, 0=безлимит)</label>
                <Input
                  type="number"
                  value={editing.traffic_limit_bytes ? Math.round(editing.traffic_limit_bytes / 1073741824) : 0}
                  onChange={e => {
                    const gb = parseInt(e.target.value) || 0
                    setEditing({...editing, traffic_limit_bytes: gb > 0 ? gb * 1073741824 : null})
                  }}
                />
              </div>
            </div>

            <Card className="p-4 mb-4">
              <label className="text-xs text-tertiary mb-2 block">Протоколы</label>
              <div className="flex flex-wrap gap-3">
                {PROTOCOL_OPTIONS.map(proto => (
                  <label key={proto} className="flex items-center gap-1.5 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={(editing.allowed_protocols || []).includes(proto)}
                      onChange={e => {
                        const current = editing.allowed_protocols || []
                        if (e.target.checked) {
                          setEditing({...editing, allowed_protocols: [...current.filter(p => p !== proto), proto]})
                        } else {
                          setEditing({...editing, allowed_protocols: current.filter(p => p !== proto)})
                        }
                      }}
                      className="rounded"
                    />
                    <span className="text-sm uppercase text-primary">{proto}</span>
                  </label>
                ))}
              </div>
            </Card>

            <Card className="p-4 mb-4">
              <label className="text-xs text-tertiary mb-2 block">Серверы (выбрать доступные)</label>
              <div className="flex flex-wrap gap-3 max-h-32 overflow-y-auto">
                {servers.map(srv => (
                  <label key={srv.id} className="flex items-center gap-1.5 cursor-pointer text-sm">
                    <input
                      type="checkbox"
                      checked={(editing.server_ids || []).includes(srv.id)}
                      onChange={e => {
                        const current = editing.server_ids || []
                        if (e.target.checked) {
                          setEditing({...editing, server_ids: [...current, srv.id]})
                        } else {
                          setEditing({...editing, server_ids: current.filter(id => id !== srv.id)})
                        }
                      }}
                      className="rounded"
                    />
                    <span className="text-primary">{srv.name || srv.ip_address}</span>
                  </label>
                ))}
              </div>
            </Card>

            <Card className="p-4 mb-4">
              <label className="text-xs text-tertiary mb-2 block">Возможности плана</label>
              <div className="flex flex-wrap gap-3">
                {[
                  { key: 'traffic_masking', label: 'Маскировка трафика' },
                  { key: 'priority_support', label: 'Приоритетная поддержка' },
                ].map(feat => {
                  const features = typeof editing.features === 'string'
                    ? (() => { try { return JSON.parse(editing.features || '{}') } catch { return {} } })()
                    : (editing.features || {})
                  return (
                    <label key={feat.key} className="flex items-center gap-1.5 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={!!features[feat.key]}
                        onChange={e => {
                          const updated = { ...features, [feat.key]: e.target.checked || undefined }
                          if (!e.target.checked) delete updated[feat.key]
                          setEditing({...editing, features: JSON.stringify(updated)})
                        }}
                        className="rounded"
                      />
                      <span className="text-sm text-primary">{feat.label}</span>
                    </label>
                  )
                })}
              </div>
            </Card>

            <Card className="p-4 mb-4">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={editing.is_active}
                    onChange={e => setEditing({...editing, is_active: e.target.checked})}
                    className="rounded"
                  />
                  <span className="text-sm text-primary">Активен</span>
                </label>
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={editing.auto_renew_default}
                    onChange={e => setEditing({...editing, auto_renew_default: e.target.checked})}
                    className="rounded"
                  />
                  <span className="text-sm text-primary">Автопродление</span>
                </label>
                <label className="flex items-start gap-2 md:col-span-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={!!editing.direct_routing_enabled}
                    onChange={e => setEditing({...editing, direct_routing_enabled: e.target.checked})}
                    className="rounded mt-1"
                  />
                  <span>
                    <span className="block text-sm text-primary">Доступ к РФ-сервисам</span>
                    <span className="block text-xs text-tertiary">
                      Трафик к российским банкам, госуслугам и маркетплейсам идёт напрямую, минуя VPN.
                    </span>
                  </span>
                </label>
                <div>
                  <label className="text-xs text-tertiary block mb-1">Порядок</label>
                  <Input
                    type="number"
                    value={editing.sort_order}
                    onChange={e => setEditing({...editing, sort_order: parseInt(e.target.value) || 0})}
                  />
                </div>
                <div>
                  <label className="text-xs text-tertiary block mb-1">Макс. активаций/юзер</label>
                  <Input
                    type="number"
                    value={editing.max_activations_per_user ?? ''}
                    placeholder="без лимита"
                    onChange={e => {
                      const v = e.target.value === '' ? null : parseInt(e.target.value) || null
                      setEditing({...editing, max_activations_per_user: v})
                    }}
                  />
                </div>
              </div>
            </Card>

            <div className="flex justify-end gap-2 pt-4 border-t border-default">
              <Button variant="ghost" onClick={() => setEditing(null)}>Отмена</Button>
              <Button variant="primary" onClick={handleSave} loading={saving}>Сохранить</Button>
            </div>
          </>
        )}
      </Dialog>

      {/* Delete confirm */}
      {confirm && (
        <ConfirmDialog
          open={true}
          title="Удалить план?"
          message={`Удалить "${confirm.name}"? Пользователи с этим планом потеряют доступ.`}
          onConfirm={() => handleDelete(confirm)}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}
