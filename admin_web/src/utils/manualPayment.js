// Manual payments (C7) as pure rules (node --test): the body of POST/PATCH /payments.

export const MANUAL_STATUSES = [
  { value: 'succeeded', label: 'Оплачен' },
  { value: 'pending', label: 'Ожидает' },
  { value: 'canceled', label: 'Отменён' },
]

export function manualPaymentBody(form, mode) {
  const amount = Number(String(form.amount ?? '').trim().replace(',', '.'))
  if (!Number.isFinite(amount) || amount <= 0 || amount > 1000000) return { body: null, error: 'Сумма — от 0,01 до 1 000 000 ₽' }
  if (!MANUAL_STATUSES.some((m) => m.value === form.status)) return { body: null, error: 'Выберите статус' }
  const body = { amount: Math.round(amount * 100) / 100, status: form.status, notes: String(form.notes || '').trim() }
  if (mode === 'create') {
    const id = Number(String(form.userDbId || '').trim())
    if (!Number.isInteger(id) || id <= 0) return { body: null, error: 'Укажите ID пользователя в базе (число из карточки)' }
    body.user_db_id = id
  }
  return { body, error: '' }
}
