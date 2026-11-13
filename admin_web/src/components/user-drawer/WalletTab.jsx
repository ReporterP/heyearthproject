import { useState, useEffect, useCallback } from 'react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { fetchUserWallet, adjustUserWallet } from '../../api/admin.js'
import { Button, Input, Badge, Skeleton, EmptyState } from '../ui/index.js'
import { formatKop, newRequestId } from '../../utils/wallet.js'

// The user's wallet (W6): balance, ledger and — with wallet.adjust — a manual credit or
// debit with a mandatory reason (written to the audit log).
export default function WalletTab({ user }) {
  const { can } = useAuth()
  const toast = useToast()
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [direction, setDirection] = useState('credit')
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [requestId, setRequestId] = useState(newRequestId)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    fetchUserWallet(user.id).then(d => { setData(d); setError('') }).catch(e => setError(e.message))
  }, [user.id])
  useEffect(() => { load() }, [load])

  async function submit(e) {
    e.preventDefault()
    const rub = Number(String(amount).replace(',', '.'))
    if (!(rub > 0)) { toast.error('Укажите сумму'); return }
    if (!reason.trim()) { toast.error('Укажите причину'); return }
    setBusy(true)
    try {
      await adjustUserWallet(user.id, { direction, amountRub: rub, reason: reason.trim(), requestId })
      toast.success(direction === 'credit' ? 'Начислено' : 'Списано')
      setAmount(''); setReason(''); setRequestId(newRequestId())
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  if (error) return <p className="text-sm text-danger">{error}</p>
  if (!data) return <Skeleton className="h-24 w-full" />

  return (
    <div className="space-y-5">
      <div className="rounded-md border border-default bg-elevated p-4">
        <p className="text-xs text-tertiary">Баланс кошелька</p>
        <p className="text-2xl font-mono tabular-nums text-primary mt-1">{formatKop(data.balance_kop)}</p>
      </div>

      {can('wallet.adjust') && (
        <form onSubmit={submit} className="rounded-md border border-default p-4 space-y-3">
          <div className="flex gap-1">
            {[['credit', 'Начислить'], ['debit', 'Списать']].map(([v, label]) => (
              <button key={v} type="button" onClick={() => setDirection(v)}
                className={`px-3 h-8 text-sm rounded border ${direction === v ? 'border-accent text-accent bg-accent/10' : 'border-default text-secondary hover:text-primary'}`}>
                {label}
              </button>
            ))}
          </div>
          <div className="flex flex-col sm:flex-row gap-2">
            <Input inputMode="decimal" placeholder="Сумма, ₽" value={amount} onChange={e => setAmount(e.target.value)} aria-label="Сумма, ₽" className="font-mono sm:!w-32" />
            <Input placeholder="Причина (обязательно, видна клиенту)" value={reason} onChange={e => setReason(e.target.value)} maxLength={300} aria-label="Причина" />
          </div>
          <Button type="submit" size="sm" variant={direction === 'debit' ? 'danger' : 'primary'} loading={busy}>
            {direction === 'credit' ? 'Начислить' : 'Списать'}
          </Button>
        </form>
      )}

      <div>
        <p className="text-xs text-tertiary mb-2">Проводки</p>
        {data.transactions.length === 0 ? (
          <EmptyState title="Пока нет движений" description="Пополнения, оплаты и начисления появятся здесь." />
        ) : (
          <ul className="divide-y divide-[var(--color-border-default)] border border-default rounded-md">
            {data.transactions.map(tx => (
              <li key={tx.id} className="flex items-center justify-between gap-3 px-3 h-9 text-sm">
                <span className="truncate text-primary" title={tx.title}>{tx.title}</span>
                <span className="flex items-center gap-3 shrink-0">
                  <Badge variant={tx.amount_kop > 0 ? 'success' : 'default'}>{tx.kind}</Badge>
                  <span className={`font-mono tabular-nums ${tx.amount_kop > 0 ? 'text-success' : 'text-primary'}`}>
                    {tx.amount_kop > 0 ? '+' : '−'}{formatKop(Math.abs(tx.amount_kop))}
                  </span>
                  <span className="font-mono tabular-nums text-tertiary text-xs w-36 text-right">{new Date(tx.created_at).toLocaleString('ru-RU')}</span>
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
