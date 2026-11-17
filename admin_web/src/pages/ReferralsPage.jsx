import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Gift } from 'lucide-react'
import { apiFetch } from '../api/client.js'
import { formatKop } from '../utils/wallet.js'
import { PageHeader, Table, Thead, Tbody, Tr, Th, Td, Badge, EmptyState, TableSkeleton, Card, CardBody } from '../components/ui/index.js'

// «Приведи друга» (cleanup plan R4): who brings friends, what was paid out, and inviters
// whose friends came from one address.
export default function ReferralsPage() {
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  useEffect(() => { apiFetch('/referrals').then(setData).catch(e => setError(e.message)) }, [])

  if (error) return <p className="text-danger text-sm">{error}</p>
  const st = data?.settings
  return (
    <div className="space-y-6">
      <PageHeader
        title="Рефералы"
        subtitle={st ? (st.enabled
          ? `Друг оплатил подписку — ${formatKop(st.referrer_kop)} пригласившему и ${formatKop(st.friend_kop)} другу. Суммы — в «Настройках».`
          : 'Программа выключена в «Настройках»') : ''}
      />
      {!data ? <TableSkeleton rows={6} cols={4} /> : (
        <>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            {[['Пришли по приглашению', data.totals.attracted], ['Оплатили (выплачено)', data.totals.rewards], ['Выплачено бонусов', formatKop(data.totals.paid_out_kop)]].map(([l, v]) => (
              <Card key={l}><CardBody><p className="text-xs text-tertiary">{l}</p><p className="text-xl font-mono tabular-nums mt-1">{v}</p></CardBody></Card>
            ))}
          </div>

          <section>
            <h2 className="text-sm font-medium text-secondary mb-2">Подозрительные: 3+ друга с одного адреса</h2>
            {data.suspicious.length === 0 ? (
              <EmptyState icon={<Gift className="w-8 h-8" />} title="Ничего подозрительного" description="Здесь появятся пригласившие, чьи друзья пришли с одного IP." />
            ) : (
              <Table>
                <Thead><Tr><Th>Пригласивший</Th><Th>IP</Th><Th align="right">Друзей</Th></Tr></Thead>
                <Tbody>
                  {data.suspicious.map(s => (
                    <Tr key={`${s.user_db_id}-${s.ip}`}>
                      <Td mono><Link className="text-accent" to={`/users?user=${s.user_db_id}`}>{s.user_id}</Link></Td>
                      <Td mono>{s.ip}</Td>
                      <Td mono align="right">{s.friends}</Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
            )}
          </section>

          <section>
            <h2 className="text-sm font-medium text-secondary mb-2">Топ пригласивших</h2>
            {data.top.length === 0 ? (
              <EmptyState icon={<Gift className="w-8 h-8" />} title="Пока никого" description="Ссылка «Пригласи друга» есть у каждого клиента в кабинете." />
            ) : (
              <Table>
                <Thead><Tr><Th>Пользователь</Th><Th align="right">Пришли</Th><Th align="right">Оплатили</Th><Th align="right">Заработал</Th></Tr></Thead>
                <Tbody>
                  {data.top.map(t => (
                    <Tr key={t.user_db_id}>
                      <Td mono><Link className="text-accent" to={`/users?user=${t.user_db_id}`}>{t.user_id}</Link></Td>
                      <Td mono align="right">{t.invited}</Td>
                      <Td mono align="right">{t.paid}</Td>
                      <Td mono align="right">{formatKop(t.earned_kop)}</Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
            )}
          </section>

          <section>
            <h2 className="text-sm font-medium text-secondary mb-2">Последние выплаты</h2>
            {data.recent.length === 0 ? (
              <EmptyState icon={<Gift className="w-8 h-8" />} title="Выплат ещё не было" description="Бонус начисляется, когда приглашённый впервые оплачивает подписку." />
            ) : (
              <Table>
                <Thead><Tr><Th>Когда</Th><Th>Пригласивший</Th><Th>Друг</Th><Th>Статус</Th><Th align="right">Бонусы</Th></Tr></Thead>
                <Tbody>
                  {data.recent.map(r => (
                    <Tr key={r.id}>
                      <Td mono>{new Date(r.created_at).toLocaleString('ru-RU')}</Td>
                      <Td mono>{r.referrer}</Td>
                      <Td mono>{r.friend}</Td>
                      <Td><Badge variant={r.status === 'paid' ? 'success' : 'warning'}>{r.status === 'paid' ? 'выплачено' : 'лимит месяца'}</Badge></Td>
                      <Td mono align="right">{formatKop(r.referrer_kop)} + {formatKop(r.friend_kop)}</Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
            )}
          </section>
        </>
      )}
    </div>
  )
}
