import { Badge, Card, CardBody, CardHeader, EmptyState } from '../ui/index.js'
import { clientKindsSummary } from './flagsModel.js'

/**
 * ClientKindsCard — the subscription as each client app gets it (hourly «clients» runs):
 * how many lines, how many the core accepts, whether data passes, when checked.
 * Props: runs (GET /api/admin/ops/probes .runs), formatTime(iso).
 */
export default function ClientKindsCard({ runs, formatTime }) {
  const kinds = clientKindsSummary(runs)
  return (
    <Card>
      <CardHeader className="text-sm font-medium text-primary">Подписка глазами приложений</CardHeader>
      <CardBody className="p-0">
        {kinds.length === 0 ? (
          <EmptyState className="py-6" title="Проверки ещё не было" description="Раз в час подписка запрашивается так, как её получают Happ, v2rayTun, INCY, Hiddify и Clash." />
        ) : (
          <ul className="divide-y divide-default">
            {kinds.map(k => (
              <li key={k.name} className="px-4 py-2 flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3 text-sm">
                <span className="text-primary sm:w-28 shrink-0">{k.name}</span>
                <span className="text-secondary">строк <span className="font-mono tabular-nums">{k.lines}</span>, ядро принимает <span className="font-mono tabular-nums">{k.accepted}</span></span>
                {k.bulkOK !== null && <Badge variant={k.bulkOK ? 'success' : 'danger'}>{k.bulkOK ? 'данные идут' : 'данные не идут'}</Badge>}
                {k.accepted < k.lines && <Badge variant="danger">не принято: {k.lines - k.accepted}</Badge>}
                <span className="text-xs text-tertiary sm:ml-auto">{formatTime(k.at)}</span>
              </li>
            ))}
          </ul>
        )}
      </CardBody>
    </Card>
  )
}
