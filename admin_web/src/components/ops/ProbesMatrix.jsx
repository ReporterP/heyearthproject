import { Card, EmptyState, Table, Thead, Tbody, Tr, Th, Td } from '../ui/index.js'
import { formatDate } from '../../utils/format.js'
import { groupProbes } from './opsModel.js'

const HISTORY = 24 // points shown per check

function Dots({ history }) {
  const shown = history.slice(0, HISTORY)
  const fails = shown.filter(h => !h.ok).length
  return (
    <span className="inline-flex items-center gap-0.5" role="img"
      aria-label={`${shown.length} прогонов, не прошли ${fails}`}>
      {[...shown].reverse().map((h) => (
        <span key={h.runId} title={`${formatDate(h.at)}${h.ok ? '' : ` — ${h.error || 'не прошло'}`}`}
          className={`h-3 w-1.5 rounded-[1px] ${h.ok ? 'bg-success/70' : 'bg-danger'}`} />
      ))}
    </span>
  )
}

/**
 * ProbesMatrix — what the synthetic client saw: one row per check (a way's download, or a
 * line's own target), its last result and its history.
 * Props: runs (as the API gives them), vantage.
 */
export default function ProbesMatrix({ runs, vantage = 'outside' }) {
  const rows = groupProbes(runs, vantage)
  if (rows.length === 0) {
    return (
      <Card>
        <EmptyState className="py-10" title="Проверок пока нет"
          description="Синтетический клиент делает прогон через 90 секунд после запуска API и затем каждые 10 минут. Слепые прогоны (нет живого выхода или подписки тестового пользователя) результатов не дают." />
      </Card>
    )
  }
  let lastLine = ''
  return (
    <Table>
      <Thead>
        <Tr><Th>Строка подписки</Th><Th>Проверка</Th><Th>Сейчас</Th><Th align="right">Время</Th><Th>История</Th><Th>Последняя ошибка</Th></Tr>
      </Thead>
      <Tbody>
        {rows.map((r) => {
          const first = r.line !== lastLine
          lastLine = r.line
          const lastError = r.history.find(h => !h.ok)
          return (
            <Tr key={r.key}>
              <Td className="whitespace-nowrap text-primary">{first ? r.line : ''}</Td>
              <Td className="whitespace-nowrap">
                <span className="text-primary">{r.label}</span>
                {r.way && <span className="ml-2 font-mono text-xs text-tertiary">{r.way}</span>}
              </Td>
              <Td>{r.last.ok ? <span className="text-success">прошла</span> : <span className="text-danger">не прошла</span>}</Td>
              <Td mono align="right" className="text-secondary">{r.last.ms ? `${r.last.ms} мс` : '—'}</Td>
              <Td><Dots history={r.history} /></Td>
              <Td className="max-w-[22rem]">
                {lastError
                  ? <span className="block truncate text-secondary" title={`${formatDate(lastError.at)}: ${lastError.error}`}>{lastError.error || 'не прошла'}</span>
                  : <span className="text-tertiary">—</span>}
              </Td>
            </Tr>
          )
        })}
      </Tbody>
    </Table>
  )
}
