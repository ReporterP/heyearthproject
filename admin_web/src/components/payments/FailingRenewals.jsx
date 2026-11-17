import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { RotateCcw } from 'lucide-react'
import { fetchFailingRenewals } from '../../api/admin.js'
import { formatKop } from '../../utils/wallet.js'
import { formatDate } from '../../utils/format.js'
import { Badge, EmptyState, Table, Thead, Tbody, Tr, Th, Td, TableSkeleton } from '../ui/index.js'

/**
 * FailingRenewals — «Платежи → Не прошло продление»: paid subscriptions with auto-renew that the
 * wallet cannot renew (ended in the last 7 days or ending within 3). The client sees «пополните
 * кошелёк» in the cabinet and the app; here the owner sees whom to remind.
 */
export default function FailingRenewals() {
  const [state, setState] = useState({ items: null, error: '' })
  useEffect(() => {
    let alive = true
    fetchFailingRenewals()
      .then((d) => { if (alive) setState({ items: d.items || [], error: '' }) })
      .catch((e) => { if (alive) setState({ items: null, error: e?.message || 'ошибка' }) })
    return () => { alive = false }
  }, [])
  if (state.error) return <p role="alert" className="text-sm text-danger">Не удалось загрузить: {state.error}</p>
  if (!state.items) return <TableSkeleton rows={4} cols={6} />
  if (state.items.length === 0) {
    return <EmptyState icon={<RotateCcw className="w-8 h-8" />} title="Всем хватает на продление"
      description="Здесь появятся подписки с автопродлением, которым не хватает денег в кошельке: закончились за неделю или закончатся за 3 дня." />
  }
  return (
    <>
      <p className="text-xs text-tertiary mb-3">Автопродление списывает с кошелька за сутки до конца подписки. Этим клиентам не хватает — им показывается «Пополните кошелёк».</p>
      <Table>
        <Thead>
          <Tr><Th>Пользователь</Th><Th>Тариф</Th><Th>Подписка до</Th><Th align="right">Цена</Th><Th align="right">На кошельке</Th><Th align="right">Не хватает</Th></Tr>
        </Thead>
        <Tbody>
          {state.items.map((it) => (
            <Tr key={it.user_db_id}>
              <Td><Link to={`/users?open=${it.user_db_id}&tab=wallet&search=${encodeURIComponent(it.user_id)}`} className="font-mono text-primary hover:text-accent">{it.user_id}</Link></Td>
              <Td>{it.plan}</Td>
              <Td mono className="whitespace-nowrap">{formatDate(it.valid_until)} {it.ended ? <Badge variant="danger">закончилась</Badge> : <Badge variant="warning">скоро</Badge>}</Td>
              <Td mono align="right">{formatKop(it.price_kop)}</Td>
              <Td mono align="right">{formatKop(it.balance_kop)}</Td>
              <Td mono align="right" className="text-warning">{formatKop(it.shortfall_kop)}</Td>
            </Tr>
          ))}
        </Tbody>
      </Table>
    </>
  )
}
