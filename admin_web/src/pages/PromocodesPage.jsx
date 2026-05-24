import { useState, useEffect } from 'react'
import { Ticket } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { useUrlState } from '../hooks/useUrlState.js'
import { fetchPromocodesPaged, createPromocode, updatePromocode, deletePromocode, fetchPlans } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
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
  Select,
  Table,
  Tbody,
  Td,
  Th,
  Thead,
  Tr,
} from '../components/ui/index.js'

const PROMO_TYPES = [
  { value: 'discount_percent', label: 'Скидка (%)' },
  { value: 'discount_fixed', label: 'Скидка (₽)' },
  { value: 'free_trial', label: 'Бесплатный период' },
  { value: 'traffic_gift', label: 'Подарок трафика' },
]

const STATUS_FILTERS = [
  { value: '', label: 'Все' },
  { value: 'active', label: 'Активные' },
  { value: 'inactive', label: 'Выключенные' },
]

export default function PromocodesPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchPromocodesPaged)
  const [filter, setFilter] = useUrlState({ status: '' })

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

  const allItems = paged.items || []
  const items = filter.status === 'active'
    ? allItems.filter(i => i.is_active)
    : filter.status === 'inactive'
      ? allItems.filter(i => !i.is_active)
      : allItems

  return (
    <div className="space-y-4">
      <PageHeader
        title="Промокоды"
        subtitle="Скидки, бесплатные периоды, подарки трафика — привязка к конкретным планам"
        actions={can('promocodes.write') ? (
          <Button variant="primary" onClick={openCreate}>+ Создать промокод</Button>
        ) : null}
      />

      <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-2">
        {STATUS_FILTERS.map(sf => (
          <Button
            key={sf.value}
            size="sm"
            variant={filter.status === sf.value ? 'primary' : 'secondary'}
            onClick={() => setFilter({ status: sf.value })}
            className="w-full sm:w-auto"
          >
            {sf.label}
          </Button>
        ))}
      </div>

      <SearchPaginationBar
        search={paged.search}
        onSearch={paged.doSearch}
        page={paged.page}
        totalPages={paged.totalPages}
        onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {items.length === 0 ? (
        <EmptyState
          icon={<Ticket className="w-12 h-12" />}
          title="Нет промокодов"
          description="Создайте первый промокод — скидку, бесплатный период или подарок трафика."
          action={can('promocodes.write') ? (
            <Button variant="primary" onClick={openCreate}>+ Создать промокод</Button>
          ) : null}
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th>Код</Th>
              <Th>Тип</Th>
              <Th>Эффект</Th>
              <Th align="center">Использ.</Th>
              <Th align="center">На юзера</Th>
              <Th>Планы</Th>
              <Th align="center">Статус</Th>
              <Th>Действия</Th>
            </Tr>
          </Thead>
          <Tbody>
            {items.map(item => (
              <Tr key={item.code}>
                <Td mono className="text-accent font-semibold">{item.code}</Td>
                <Td><Badge variant="default">{promoTypeLabel(item.promo_type)}</Badge></Td>
                <Td className="font-medium">{promoEffect(item)}</Td>
                <Td align="center" mono>{item.current_uses}/{item.max_uses}</Td>
                <Td align="center" mono>{item.per_user_limit}</Td>
                <Td className="text-xs text-tertiary">
                  {item.applies_to_plans?.length > 0
                    ? item.applies_to_plans.map(id => plans.find(p => p.id === id)?.name || `#${id}`).join(', ')
                    : <span className="text-success">Все</span>}
                </Td>
                <Td align="center">
                  {item.is_active
                    ? <Badge variant="success">активен</Badge>
                    : <Badge variant="warning">выключен</Badge>}
                </Td>
                <Td>
                  {can('promocodes.write') && (
                    <div className="flex items-center gap-1">
                      <Button size="sm" variant="ghost" onClick={() => openEdit(item)}>Ред.</Button>
                      <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirm(item)}>Уд.</Button>
                    </div>
                  )}
                </Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}

      {/* Create / Edit dialog */}
      <Dialog
        open={!!editing}
        onClose={() => setEditing(null)}
        title={editing?._existing ? `Редактировать ${editing.code}` : 'Новый промокод'}
        maxWidth="max-w-2xl"
      >
        {editing && (
          <>
            <div className="grid grid-cols-2 gap-4 mb-4">
              <div>
                <label className="text-xs text-tertiary block mb-1">Код</label>
                <Input
                  value={editing.code}
                  onChange={e => setEditing({...editing, code: e.target.value.toUpperCase()})}
                  disabled={editing._existing}
                  className="font-mono"
                />
              </div>
              <div>
                <label className="text-xs text-tertiary block mb-1">Тип промокода</label>
                <Select
                  value={editing.promo_type}
                  onChange={e => setEditing({...editing, promo_type: e.target.value})}
                >
                  {PROMO_TYPES.map(pt => <option key={pt.value} value={pt.value}>{pt.label}</option>)}
                </Select>
              </div>
            </div>

            {/* Dynamic fields by type */}
            <Card className="p-4 mb-4">
              {editing.promo_type === 'discount_percent' && (
                <div>
                  <label className="text-xs text-tertiary block mb-1">Размер скидки (%)</label>
                  <Input
                    type="number"
                    value={editing.discount_percent}
                    onChange={e => setEditing({...editing, discount_percent: parseInt(e.target.value) || 0})}
                    min={1}
                    max={100}
                  />
                  <p className="text-[10px] text-tertiary mt-1">Например: 50 = скидка 50% на стоимость плана</p>
                </div>
              )}
              {editing.promo_type === 'discount_fixed' && (
                <div>
                  <label className="text-xs text-tertiary block mb-1">Размер скидки (₽)</label>
                  <Input
                    type="number"
                    value={editing.discount_fixed_rub}
                    onChange={e => setEditing({...editing, discount_fixed_rub: parseInt(e.target.value) || 0})}
                  />
                  <p className="text-[10px] text-tertiary mt-1">Фиксированная скидка в рублях</p>
                </div>
              )}
              {editing.promo_type === 'free_trial' && (
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="text-xs text-tertiary block mb-1">Бесплатных дней</label>
                    <Input
                      type="number"
                      value={editing.free_days}
                      onChange={e => setEditing({...editing, free_days: parseInt(e.target.value) || 7})}
                    />
                  </div>
                  <div>
                    <label className="text-xs text-tertiary block mb-1">Какой план выдать</label>
                    <Select
                      value={editing.plan_id_granted || ''}
                      onChange={e => setEditing({...editing, plan_id_granted: parseInt(e.target.value) || null})}
                    >
                      <option value="">— выберите план —</option>
                      {plans.map(p => <option key={p.id} value={p.id}>{p.name} ({p.price_rub}₽)</option>)}
                    </Select>
                  </div>
                </div>
              )}
              {editing.promo_type === 'traffic_gift' && (
                <div>
                  <label className="text-xs text-tertiary block mb-1">Подарок трафика (ГБ)</label>
                  <Input
                    type="number"
                    value={editing.traffic_gift_bytes ? Math.round(editing.traffic_gift_bytes / 1073741824) : 0}
                    onChange={e => setEditing({...editing, traffic_gift_bytes: (parseInt(e.target.value) || 0) * 1073741824})}
                  />
                  <p className="text-[10px] text-tertiary mt-1">Добавляется к трафику подписки</p>
                </div>
              )}
            </Card>

            <div className="grid grid-cols-3 gap-4 mb-4">
              <div>
                <label className="text-xs text-tertiary block mb-1">Макс. использований</label>
                <Input
                  type="number"
                  value={editing.max_uses}
                  onChange={e => setEditing({...editing, max_uses: parseInt(e.target.value) || 1})}
                />
              </div>
              <div>
                <label className="text-xs text-tertiary block mb-1">Лимит на юзера</label>
                <Input
                  type="number"
                  value={editing.per_user_limit}
                  onChange={e => setEditing({...editing, per_user_limit: parseInt(e.target.value) || 1})}
                />
              </div>
              <div>
                <label className="text-xs text-tertiary block mb-1">Срок действия</label>
                <Input
                  type="date"
                  value={editing.expires_at ? editing.expires_at.slice(0, 10) : ''}
                  onChange={e => setEditing({...editing, expires_at: e.target.value ? e.target.value + 'T23:59:59Z' : ''})}
                />
              </div>
            </div>

            {/* Applies to plans */}
            <div className="mb-4">
              <label className="text-xs text-tertiary mb-2 block">
                Действует на планы <span className="text-disabled">(пусто = все планы)</span>
              </label>
              <div className="flex flex-wrap gap-2">
                {plans.map(p => (
                  <label key={p.id} className="flex items-center gap-1.5 cursor-pointer text-sm bg-elevated/40 px-3 py-1.5 rounded border border-default">
                    <input
                      type="checkbox"
                      checked={(editing.applies_to_plans || []).includes(p.id)}
                      onChange={e => {
                        const arr = editing.applies_to_plans || []
                        if (e.target.checked) setEditing({...editing, applies_to_plans: [...arr, p.id]})
                        else setEditing({...editing, applies_to_plans: arr.filter(id => id !== p.id)})
                      }}
                      className="rounded"
                    />
                    <span className="text-primary">{p.name}</span>
                    <span className="text-tertiary text-[10px]">{p.price_rub}₽</span>
                  </label>
                ))}
              </div>
            </div>

            <label className="flex items-center gap-2 cursor-pointer mb-4">
              <input
                type="checkbox"
                checked={editing.is_active}
                onChange={e => setEditing({...editing, is_active: e.target.checked})}
                className="rounded"
              />
              <span className="text-sm text-primary">Активен</span>
            </label>

            <div className="flex justify-end gap-2 pt-4 border-t border-default">
              <Button variant="ghost" onClick={() => setEditing(null)}>Отмена</Button>
              <Button variant="primary" onClick={handleSave} loading={saving}>
                Сохранить
              </Button>
            </div>
          </>
        )}
      </Dialog>

      {confirm && (
        <ConfirmDialog
          open={true}
          title="Удалить промокод?"
          message={`Удалить "${confirm.code}"?`}
          onConfirm={() => handleDelete(confirm)}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}
