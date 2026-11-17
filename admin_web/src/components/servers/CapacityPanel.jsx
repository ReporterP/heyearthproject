import { Link } from 'react-router-dom'
import { CheckCircle2, TriangleAlert, Gauge, ArrowRight } from 'lucide-react'
import { Badge, Card, CardBody, CardHeader, Table, Thead, Tbody, Tr, Th, Td } from '../ui/index.js'
import { VERDICT_TONE, ROLE_TITLE, capacityNodeRow, capacitySummary } from './serversModel.js'

const TONE_BOX = {
  success: 'border-success/30 bg-success/10',
  warning: 'border-warning/30 bg-warning/10',
  default: 'border-default bg-elevated',
}
const ICON_TONE = { success: 'text-success', warning: 'text-warning', default: 'text-tertiary' }

function Verdict({ verdict, big = false }) {
  const tone = VERDICT_TONE[verdict?.level] || 'default'
  const Icon = tone === 'success' ? CheckCircle2 : tone === 'warning' ? TriangleAlert : Gauge
  return (
    <div role="status" className={`flex items-start gap-2 rounded border px-3 py-2 ${TONE_BOX[tone]}`}>
      <Icon className={`w-4 h-4 mt-0.5 shrink-0 ${ICON_TONE[tone]}`} aria-hidden="true" />
      <span className={`${big ? 'text-sm' : 'text-xs'} text-primary min-w-0`}>{verdict?.text || 'Нет данных'}</span>
    </div>
  )
}

function Cell({ v }) {
  return <span className={v.warn ? 'text-warning' : ''}>{v.text}</span>
}

function GroupCard({ g }) {
  const relay = g.role === 'relay'
  return (
    <Card>
      <CardHeader className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
        <h3 className="text-sm font-semibold text-primary">{ROLE_TITLE[g.role] || g.label}</h3>
        <Badge variant={VERDICT_TONE[g.verdict?.level] || 'default'}>{g.verdict?.level === 'ok' ? 'в норме' : g.verdict?.level === 'warn' ? 'внимание' : 'нет данных'}</Badge>
      </CardHeader>
      <CardBody className="space-y-3">
        <Verdict verdict={g.verdict} />
        {g.n_plus_1 && <p className={`text-xs ${g.n_plus_1.level === 'warn' ? 'text-warning' : 'text-secondary'}`}>Запас на отказ одного: {g.n_plus_1.text}</p>}
        {g.forecast && <p className="text-xs text-secondary">Прогноз: {g.forecast}</p>}
        {g.buy && <p className="text-xs text-primary">Что докупить: <span className="text-accent">{g.buy}</span></p>}
        {g.nodes?.length > 0 && (
          <Table>
            <Thead>
              <Tr>
                <Th>Сервер</Th>
                <Th align="right">CPU, пик</Th>
                <Th align="right">Канал, пик</Th>
                {relay && <Th align="right">conntrack</Th>}
                <Th align="right">Трафик за месяц</Th>
                <Th align="right">Цена</Th>
              </Tr>
            </Thead>
            <Tbody>
              {g.nodes.map((n) => {
                const r = capacityNodeRow(n)
                return (
                  <Tr key={`${g.role}-${n.id}`}>
                    <Td>
                      <span className="text-primary">{r.name}</span>
                      {r.noData && <span className="block text-[11px] text-tertiary">нет данных за сутки</span>}
                    </Td>
                    <Td mono align="right"><Cell v={r.cpu} /></Td>
                    <Td mono align="right"><Cell v={r.net} /></Td>
                    {relay && <Td mono align="right"><Cell v={r.conntrack} /></Td>}
                    <Td mono align="right"><Cell v={r.month} /></Td>
                    <Td mono align="right">{r.price}</Td>
                  </Tr>
                )
              })}
            </Tbody>
          </Table>
        )}
      </CardBody>
    </Card>
  )
}

/**
 * CapacityPanel — «Мощность» (GET /capacity): the verdict in words, each role's thresholds,
 * the N+1 reserve, the 14-day forecast, what to buy and the cost per active user.
 * compact: the dashboard widget (verdict, money, a link to the tab).
 */
export default function CapacityPanel({ rep, compact = false }) {
  if (!rep) return null
  const sum = capacitySummary(rep)
  if (compact) {
    return (
      <Card>
        <CardHeader className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-semibold text-primary">Мощность</h3>
          <Link to="/servers?tab=capacity" className="inline-flex items-center gap-1 text-xs text-accent hover:underline">
            Подробно <ArrowRight className="w-3 h-3" aria-hidden="true" />
          </Link>
        </CardHeader>
        <CardBody className="space-y-2">
          <Verdict verdict={rep.verdict} big />
          <div className="flex flex-col sm:flex-row sm:flex-wrap gap-x-4 gap-y-1 text-xs text-secondary">
            <span>{sum.cost}</span>
            {sum.perUser && <span className="font-mono tabular-nums">{sum.perUser}</span>}
            <span className={sum.usersWarn ? 'text-warning' : ''}>{sum.users}</span>
          </div>
        </CardBody>
      </Card>
    )
  }
  return (
    <div className="space-y-4">
      <Verdict verdict={rep.verdict} big />
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <Card className="p-3"><p className="text-xs text-tertiary">Серверы</p><p className="text-sm text-primary mt-1">{sum.cost}</p></Card>
        <Card className="p-3"><p className="text-xs text-tertiary">На пользователя</p><p className="text-sm text-primary font-mono tabular-nums mt-1">{sum.perUser || '—'}</p></Card>
        <Card className="p-3"><p className="text-xs text-tertiary">Пользователи</p><p className={`text-sm mt-1 ${sum.usersWarn ? 'text-warning' : 'text-primary'}`}>{sum.users}</p></Card>
      </div>
      {(rep.groups || []).map((g) => <GroupCard key={g.role} g={g} />)}
      <p className="text-xs text-tertiary">
        Пороги: выход — CPU выше 60 % или канал выше половины порта; вход — канал выше половины порта или conntrack выше 70 %;
        Hub — CPU выше 60 %; любой сервер — трафик за месяц выше 70 % лимита хостера. Пики — p95 по часам за сутки,
        прогноз — тренд дневных пиков за 14 дней. Лимит, цену, порт и ядра впишите в «Настройках» сервера.
      </p>
    </div>
  )
}
