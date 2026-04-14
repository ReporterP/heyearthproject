import { useState, useEffect } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchPromocodesPaged, createPromocode, updatePromocode, deletePromocode, fetchPlans } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import { formatBytes } from '../utils/format.js'

const PROMO_TYPES = [
  { value: 'discount_percent', label: 'Скидка (%)' },
  { value: 'discount_fixed', label: 'Скидка (₽)' },
  { value: 'free_trial', label: 'Бесплатный период' },
  { value: 'traffic_gift', label: 'Подарок трафика' },
]

export default function PromocodesPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchPromocodesPaged)

  const [plans, setPlans] = useState([])
  const [editing, setEditing] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    fetchPlans().then(p => setPlans(Array.isArray(p) ? p : [])).catch(() => {})
  }, [])

  function openCreate() {
    setEditing({
      code: '', promo_type: 'free_trial', duration_days: 30, max_uses: 100,
      discount_percent: 0, discount_fixed_rub: 0, free_days: 7,
      traffic_gift_bytes: 0, applies_to_plans: [], plan_id_granted: null,
      per_user_limit: 1, is_active: true, group_id_granted: 0, expires_at: '',
    })
  }

  function openEdit(item) {
    setEditing({ ...item, applies_to_plans: item.applies_to_plans || [], _existing: true })
  }

  async function handleSave() {
    if (!editing.code) { toast.error('Код обязателен'); return }
    setSaving(true)
    try {
      if (editing._existing) {
        await updatePromocode(editing.code, editing)
        toast.success('Промокод обновлён')
      } else {
        await createPromocode(editing)
        toast.success('Промокод создан')
      }
      setEditing(null)
      paged.reload()
    } catch (e) {
      toast.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(item) {
    try {
      await deletePromocode(item.code)
      toast.success('Удалён')
      paged.reload()
    } catch (e) {
      toast.error(e.message)
    }
    setConfirm(null)
  }

  function promoTypeLabel(type) {
    return PROMO_TYPES.find(pt => pt.value === type)?.label || type
  }

  function promoEffect(item) {
    switch (item.promo_type) {
      case 'discount_percent': return `-${item.discount_percent}%`
      case 'discount_fixed': return `-${item.discount_fixed_rub}₽`
      case 'free_trial': return `${item.free_days || item.duration_days} дн. бесплатно`
      case 'traffic_gift': return `+${formatBytes(item.traffic_gift_bytes)}`
      default: return '—'
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-2xl font-bold">Промокоды</h2>
          <p className="text-sm text-text-secondary mt-1">Скидки, бесплатные периоды, подарки трафика — привязка к конкретным планам</p>
        </div>
        {can('promocodes.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + Создать промокод
          </button>
        )}
      </div>

      <SearchPaginationBar search={paged.search} onSearch={paged.doSearch}
        page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage} loading={paged.loading} />

      <div className="overflow-x-auto rounded-lg border border-white/5 mt-4">
        <table className="w-full text-sm">
          <thead className="bg-surface-alt/30 text-text-secondary text-xs uppercase">
            <tr>
              <th className="py-2 px-3 text-left">Код</th>
              <th className="py-2 px-3 text-left">Тип</th>
              <th className="py-2 px-3 text-left">Эффект</th>
              <th className="py-2 px-3 text-center">Использ.</th>
              <th className="py-2 px-3 text-center">На юзера</th>
              <th className="py-2 px-3 text-left">Планы</th>
              <th className="py-2 px-3 text-center">Активен</th>
              <th className="py-2 px-3"></th>
            </tr>
          </thead>
          <tbody>
            {(paged.items || []).map(item => (
              <tr key={item.code} className="border-t border-white/5 hover:bg-white/5">
                <td className="py-2 px-3 font-mono font-bold text-brand">{item.code}</td>
                <td className="py-2 px-3"><span className="px-2 py-0.5 rounded text-[10px] bg-surface-alt">{promoTypeLabel(item.promo_type)}</span></td>
                <td className="py-2 px-3 font-medium">{promoEffect(item)}</td>
                <td className="py-2 px-3 text-center">{item.current_uses}/{item.max_uses}</td>
                <td className="py-2 px-3 text-center">{item.per_user_limit}</td>
                <td className="py-2 px-3 text-xs text-text-secondary">
                  {item.applies_to_plans?.length > 0
                    ? item.applies_to_plans.map(id => plans.find(p => p.id === id)?.name || `#${id}`).join(', ')
                    : <span className="text-green-400">Все</span>}
                </td>
                <td className="py-2 px-3 text-center">{item.is_active ? <span className="text-green-400">✓</span> : <span className="text-red-400">✗</span>}</td>
                <td className="py-2 px-3">
                  {can('promocodes.write') && (
                    <div className="flex gap-2">
                      <button onClick={() => openEdit(item)} className="text-brand text-xs hover:underline">Ред.</button>
                      <button onClick={() => setConfirm(item)} className="text-red-400 text-xs hover:underline">Уд.</button>
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Modal */}
      {editing && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={() => setEditing(null)}>
          <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-2xl max-h-[90vh] overflow-y-auto p-6" onClick={e => e.stopPropagation()}>
            <h3 className="text-lg font-semibold mb-4">{editing._existing ? `Редактировать ${editing.code}` : 'Новый промокод'}</h3>

            <div className="grid grid-cols-2 gap-4 mb-4">
              <div>
                <label className="text-xs text-text-secondary">Код</label>
                <input value={editing.code} onChange={e => setEditing({...editing, code: e.target.value.toUpperCase()})}
                  disabled={editing._existing}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm font-mono disabled:opacity-50" />
              </div>
              <div>
                <label className="text-xs text-text-secondary">Тип промокода</label>
                <select value={editing.promo_type} onChange={e => setEditing({...editing, promo_type: e.target.value})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm">
                  {PROMO_TYPES.map(pt => <option key={pt.value} value={pt.value}>{pt.label}</option>)}
                </select>
              </div>
            </div>

            {/* Dynamic fields by type */}
            <div className="bg-surface-alt/30 rounded-lg p-4 mb-4">
              {editing.promo_type === 'discount_percent' && (
                <div>
                  <label className="text-xs text-text-secondary">Размер скидки (%)</label>
                  <input type="number" value={editing.discount_percent} onChange={e => setEditing({...editing, discount_percent: parseInt(e.target.value) || 0})}
                    className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-bg border border-white/10 text-sm" min={1} max={100} />
                  <p className="text-[10px] text-text-secondary mt-1">Например: 50 = скидка 50% на стоимость плана</p>
                </div>
              )}
              {editing.promo_type === 'discount_fixed' && (
                <div>
                  <label className="text-xs text-text-secondary">Размер скидки (₽)</label>
                  <input type="number" value={editing.discount_fixed_rub} onChange={e => setEditing({...editing, discount_fixed_rub: parseInt(e.target.value) || 0})}
                    className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-bg border border-white/10 text-sm" />
                  <p className="text-[10px] text-text-secondary mt-1">Фиксированная скидка в рублях</p>
                </div>
              )}
              {editing.promo_type === 'free_trial' && (
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="text-xs text-text-secondary">Бесплатных дней</label>
                    <input type="number" value={editing.free_days} onChange={e => setEditing({...editing, free_days: parseInt(e.target.value) || 7})}
                      className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-bg border border-white/10 text-sm" />
                  </div>
                  <div>
                    <label className="text-xs text-text-secondary">Какой план выдать</label>
                    <select value={editing.plan_id_granted || ''} onChange={e => setEditing({...editing, plan_id_granted: parseInt(e.target.value) || null})}
                      className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-bg border border-white/10 text-sm">
                      <option value="">— выберите план —</option>
                      {plans.map(p => <option key={p.id} value={p.id}>{p.name} ({p.price_rub}₽)</option>)}
                    </select>
                  </div>
                </div>
              )}
              {editing.promo_type === 'traffic_gift' && (
                <div>
                  <label className="text-xs text-text-secondary">Подарок трафика (ГБ)</label>
                  <input type="number" value={editing.traffic_gift_bytes ? Math.round(editing.traffic_gift_bytes / 1073741824) : 0}
                    onChange={e => setEditing({...editing, traffic_gift_bytes: (parseInt(e.target.value) || 0) * 1073741824})}
                    className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-bg border border-white/10 text-sm" />
                  <p className="text-[10px] text-text-secondary mt-1">Добавляется к трафику подписки</p>
                </div>
              )}
            </div>

            <div className="grid grid-cols-3 gap-4 mb-4">
              <div>
                <label className="text-xs text-text-secondary">Макс. использований</label>
                <input type="number" value={editing.max_uses} onChange={e => setEditing({...editing, max_uses: parseInt(e.target.value) || 1})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              </div>
              <div>
                <label className="text-xs text-text-secondary">Лимит на юзера</label>
                <input type="number" value={editing.per_user_limit} onChange={e => setEditing({...editing, per_user_limit: parseInt(e.target.value) || 1})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              </div>
              <div>
                <label className="text-xs text-text-secondary">Срок действия</label>
                <input type="date" value={editing.expires_at ? editing.expires_at.slice(0, 10) : ''} onChange={e => setEditing({...editing, expires_at: e.target.value ? e.target.value + 'T23:59:59Z' : ''})}
                  className="w-full mt-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              </div>
            </div>

            {/* Applies to plans */}
            <div className="mb-4">
              <label className="text-xs text-text-secondary mb-2 block">Действует на планы <span className="text-text-secondary/50">(пусто = все планы)</span></label>
              <div className="flex flex-wrap gap-2">
                {plans.map(p => (
                  <label key={p.id} className="flex items-center gap-1.5 cursor-pointer text-sm bg-surface-alt/50 px-3 py-1.5 rounded-lg">
                    <input type="checkbox"
                      checked={(editing.applies_to_plans || []).includes(p.id)}
                      onChange={e => {
                        const arr = editing.applies_to_plans || []
                        if (e.target.checked) setEditing({...editing, applies_to_plans: [...arr, p.id]})
                        else setEditing({...editing, applies_to_plans: arr.filter(id => id !== p.id)})
                      }}
                      className="rounded" />
                    <span>{p.name}</span>
                    <span className="text-text-secondary text-[10px]">{p.price_rub}₽</span>
                  </label>
                ))}
              </div>
            </div>

            <label className="flex items-center gap-2 cursor-pointer mb-4">
              <input type="checkbox" checked={editing.is_active} onChange={e => setEditing({...editing, is_active: e.target.checked})} className="rounded" />
              <span className="text-sm">Активен</span>
            </label>

            <div className="flex justify-end gap-3 pt-4 border-t border-white/10">
              <button onClick={() => setEditing(null)} className="px-4 py-2 rounded-lg bg-surface-alt text-text-secondary text-sm">Отмена</button>
              <button onClick={handleSave} disabled={saving} className="px-6 py-2 rounded-lg bg-brand text-white text-sm font-medium disabled:opacity-50">
                {saving ? 'Сохранение...' : 'Сохранить'}
              </button>
            </div>
          </div>
        </div>
      )}

      {confirm && (
        <ConfirmDialog open={true} title="Удалить промокод?" message={`Удалить "${confirm.code}"?`}
          onConfirm={() => handleDelete(confirm)} onCancel={() => setConfirm(null)} />
      )}
    </div>
  )
}
