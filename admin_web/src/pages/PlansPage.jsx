import { useState, useEffect } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchPlans, createPlan, updatePlan, deletePlan, fetchServersPaged } from '../api/admin.js'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import { formatBytes } from '../utils/format.js'

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
      is_active: true, sort_order: 0, auto_renew_default: true, server_ids: [],
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

  if (loading) return <div className="flex justify-center py-20"><div className="w-8 h-8 border-2 border-brand/30 border-t-brand rounded-full animate-spin" /></div>

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-2xl font-bold">Тарифные планы</h2>
          <p className="text-sm text-text-secondary mt-1">Конструктор подписок: протоколы, серверы, трафик, цена</p>
        </div>
        {can('settings.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + Создать план
          </button>
        )}
      </div>

      {/* Plans grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 mb-6">
        {plans.map(plan => (
          <div key={plan.id} className={`bg-surface-card rounded-xl border p-5 transition ${plan.is_active ? 'border-white/10' : 'border-red-500/20 opacity-60'}`}>
            <div className="flex items-start justify-between mb-3">
              <div>
                <h3 className="font-semibold text-lg">{plan.name}</h3>
                <p className="text-xs text-text-secondary font-mono">{plan.slug}</p>
              </div>
              <div className="text-right">
                <p className="text-2xl font-bold text-brand">{plan.price_rub}₽</p>
                <p className="text-xs text-text-secondary">{plan.duration_days} дн.</p>
              </div>
            </div>

            <p className="text-sm text-text-secondary mb-3">{plan.description}</p>

            <div className="flex flex-wrap gap-1.5 mb-3">
              {(plan.allowed_protocols || []).map(p => (
                <span key={p} className="px-2 py-0.5 rounded text-[10px] font-bold bg-brand/10 text-brand uppercase">{p}</span>
              ))}
            </div>

            <div className="text-xs text-text-secondary space-y-1 mb-3">
              <p>Трафик: <span className="text-text-primary font-medium">{plan.traffic_limit_bytes ? formatBytes(plan.traffic_limit_bytes) : 'Безлимит'}</span></p>
              <p>Серверов: <span className="text-text-primary font-medium">{plan.server_ids?.length || 0}</span></p>
              <p>Автопродление: <span className="text-text-primary">{plan.auto_renew_default ? 'Да' : 'Нет'}</span></p>
              {!plan.is_active && <p className="text-red-400 font-medium">Неактивен</p>}
            </div>

            {can('settings.write') && (
              <div className="flex gap-2 pt-3 border-t border-white/5">
                <button onClick={() => openEdit(plan)} className="px-3 py-1.5 rounded-lg bg-brand/20 text-brand text-xs hover:bg-brand/30 transition">Редактировать</button>
                <button onClick={() => setConfirm(plan)} className="px-3 py-1.5 rounded-lg bg-red-500/20 text-red-400 text-xs hover:bg-red-500/30 transition">Удалить</button>
              </div>
            )}
          </div>
        ))}
      </div>

      {/* Edit/Create modal */}
      {editing && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={() => setEditing(null)}>
          <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-2xl max-h-[90vh] overflow-y-auto p-6" onClick={e => e.stopPropagation()}>
            <h3 className="text-lg font-semibold mb-4">{editing.id ? 'Редактировать план' : 'Создать план'}</h3>

            <div className="grid grid-cols-2 gap-4 mb-4">
              <div>
                <label className="text-xs text-text-secondary">Slug (URL-ключ)</label>
                <input value={editing.slug} onChange={e => setEditing({...editing, slug: e.target.value})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" placeholder="socks-basic" />
              </div>
              <div>
                <label className="text-xs text-text-secondary">Название</label>
                <input value={editing.name} onChange={e => setEditing({...editing, name: e.target.value})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" placeholder="SOCKS Basic" />
              </div>
            </div>

            <div className="mb-4">
              <label className="text-xs text-text-secondary">Описание</label>
              <textarea value={editing.description} onChange={e => setEditing({...editing, description: e.target.value})}
                className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" rows={2} />
            </div>

            <div className="grid grid-cols-3 gap-4 mb-4">
              <div>
                <label className="text-xs text-text-secondary">Цена (₽)</label>
                <input type="number" value={editing.price_rub} onChange={e => setEditing({...editing, price_rub: parseInt(e.target.value) || 0})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              </div>
              <div>
                <label className="text-xs text-text-secondary">Срок (дни)</label>
                <input type="number" value={editing.duration_days} onChange={e => setEditing({...editing, duration_days: parseInt(e.target.value) || 30})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              </div>
              <div>
                <label className="text-xs text-text-secondary">Трафик (ГБ, 0=безлимит)</label>
                <input type="number" value={editing.traffic_limit_bytes ? Math.round(editing.traffic_limit_bytes / 1073741824) : 0}
                  onChange={e => {
                    const gb = parseInt(e.target.value) || 0
                    setEditing({...editing, traffic_limit_bytes: gb > 0 ? gb * 1073741824 : null})
                  }}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              </div>
            </div>

            <div className="mb-4">
              <label className="text-xs text-text-secondary mb-2 block">Протоколы</label>
              <div className="flex flex-wrap gap-2">
                {PROTOCOL_OPTIONS.map(proto => (
                  <label key={proto} className="flex items-center gap-1.5 cursor-pointer">
                    <input type="checkbox"
                      checked={(editing.allowed_protocols || []).includes(proto)}
                      onChange={e => {
                        const current = editing.allowed_protocols || []
                        if (e.target.checked) {
                          setEditing({...editing, allowed_protocols: [...current.filter(p => p !== proto), proto]})
                        } else {
                          setEditing({...editing, allowed_protocols: current.filter(p => p !== proto)})
                        }
                      }}
                      className="rounded" />
                    <span className="text-sm uppercase">{proto}</span>
                  </label>
                ))}
              </div>
            </div>

            <div className="mb-4">
              <label className="text-xs text-text-secondary mb-2 block">Серверы (выбрать доступные)</label>
              <div className="flex flex-wrap gap-2 max-h-32 overflow-y-auto">
                {servers.map(srv => (
                  <label key={srv.id} className="flex items-center gap-1.5 cursor-pointer text-sm">
                    <input type="checkbox"
                      checked={(editing.server_ids || []).includes(srv.id)}
                      onChange={e => {
                        const current = editing.server_ids || []
                        if (e.target.checked) {
                          setEditing({...editing, server_ids: [...current, srv.id]})
                        } else {
                          setEditing({...editing, server_ids: current.filter(id => id !== srv.id)})
                        }
                      }}
                      className="rounded" />
                    <span>{srv.name || srv.ip_address}</span>
                  </label>
                ))}
              </div>
            </div>

            <div className="mb-4">
              <label className="text-xs text-text-secondary mb-2 block">Возможности плана</label>
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
                      <input type="checkbox"
                        checked={!!features[feat.key]}
                        onChange={e => {
                          const updated = { ...features, [feat.key]: e.target.checked || undefined }
                          if (!e.target.checked) delete updated[feat.key]
                          setEditing({...editing, features: JSON.stringify(updated)})
                        }}
                        className="rounded" />
                      <span className="text-sm">{feat.label}</span>
                    </label>
                  )
                })}
              </div>
            </div>

            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
              <label className="flex items-center gap-2 cursor-pointer">
                <input type="checkbox" checked={editing.is_active} onChange={e => setEditing({...editing, is_active: e.target.checked})} className="rounded" />
                <span className="text-sm">Активен</span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input type="checkbox" checked={editing.auto_renew_default} onChange={e => setEditing({...editing, auto_renew_default: e.target.checked})} className="rounded" />
                <span className="text-sm">Автопродление</span>
              </label>
              <div>
                <label className="text-xs text-text-secondary">Порядок</label>
                <input type="number" value={editing.sort_order} onChange={e => setEditing({...editing, sort_order: parseInt(e.target.value) || 0})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              </div>
              <div>
                <label className="text-xs text-text-secondary">Макс. активаций/юзер</label>
                <input type="number" value={editing.max_activations_per_user ?? ''} placeholder="без лимита"
                  onChange={e => {
                    const v = e.target.value === '' ? null : parseInt(e.target.value) || null
                    setEditing({...editing, max_activations_per_user: v})
                  }}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              </div>
            </div>

            <div className="flex justify-end gap-3 pt-4 border-t border-white/10">
              <button onClick={() => setEditing(null)} className="px-4 py-2 rounded-lg bg-surface-alt text-text-secondary text-sm hover:text-text-primary transition">Отмена</button>
              <button onClick={handleSave} className="px-6 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">Сохранить</button>
            </div>
          </div>
        </div>
      )}

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
