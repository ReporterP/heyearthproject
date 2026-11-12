import { useEffect, useRef, useState } from 'react'
import { Smartphone, RefreshCw } from 'lucide-react'
import { startMobileCheck, fetchMobileCheck } from '../../api/admin.js'
import { Badge, Button, CopyButton, Table, Thead, Tbody, Tr, Th, Td } from '../ui/index.js'
import { mobileCheckSummary } from './entriesModel.js'

const POLL_MS = 3000

/**
 * MobileCheckPanel — «Проверить с телефона» for one node: starts a 30-minute test page on the
 * server, shows its link and QR code, and polls what really reached each phone (bytes the phone
 * acknowledged, by operator). Props: serverId, canStart (nodes.provision), onResult(summary).
 */
export default function MobileCheckPanel({ serverId, canStart = true, onResult }) {
  const [check, setCheck] = useState(null) // { active, url, qr, expires_at, results }
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const onResultRef = useRef(onResult)
  onResultRef.current = onResult

  // The last state of a check (also one started earlier, e.g. before the wizard was reopened).
  useEffect(() => {
    let alive = true
    fetchMobileCheck(serverId).then((r) => { if (alive) setCheck(r) }).catch(() => {})
    return () => { alive = false }
  }, [serverId])

  useEffect(() => {
    if (!check?.active) return undefined
    const t = setInterval(() => {
      fetchMobileCheck(serverId).then(setCheck).catch((e) => setError(e?.message || 'Не удалось прочитать результат'))
    }, POLL_MS)
    return () => clearInterval(t)
  }, [serverId, check?.active])

  const results = check?.results || []
  const summary = mobileCheckSummary(results)
  useEffect(() => { onResultRef.current?.(summary) }, [summary.state, summary.passed, summary.failed])

  async function start() {
    setBusy(true)
    setError('')
    try {
      const r = await startMobileCheck(serverId)
      setCheck({ active: true, results: [], ...r })
    } catch (e) {
      setError(e?.message || 'Не удалось начать проверку')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-3">
      <p className="text-xs text-secondary">
        Мобильные операторы пропускают не все адреса хостингов. Проверить можно только с телефона: на сервере на 30 минут
        появится тестовая страница, вы открываете её с выключенным Wi-Fi — и видно, сколько данных реально дошло.
      </p>

      {check?.active && check.url ? (
        <div className="flex flex-col sm:flex-row gap-4 rounded border border-default bg-elevated p-3">
          {check.qr && (
            <img src={check.qr} alt="QR-код ссылки проверки" width={160} height={160}
              className="w-40 h-40 shrink-0 self-center rounded bg-white p-1" />
          )}
          <div className="min-w-0 space-y-2">
            <ol className="list-decimal pl-4 text-sm text-primary space-y-1">
              <li>Выключите на телефоне Wi-Fi — должен остаться только мобильный интернет.</li>
              <li>Наведите камеру на QR-код или откройте ссылку:</li>
            </ol>
            <div className="flex items-center gap-1">
              <code className="font-mono text-sm text-accent break-all">{check.url}</code>
              <CopyButton text={check.url} label="Скопировать ссылку проверки" />
            </div>
            <p className="text-xs text-tertiary">Подождите 20–30 секунд. Лучше проверить SIM-картами разных операторов — результат появится ниже сам.</p>
          </div>
        </div>
      ) : (
        canStart && (
          <Button variant="secondary" icon={<Smartphone className="w-3.5 h-3.5" />} loading={busy} onClick={start}>
            {results.length ? 'Проверить ещё раз' : 'Начать проверку с телефона'}
          </Button>
        )
      )}

      {error && <p role="alert" className="text-xs text-danger break-words">{error}</p>}

      {results.length > 0 && (
        <>
          <div role="status" className={`rounded border px-3 py-2 text-sm ${summary.state === 'pass'
            ? 'border-success/30 bg-success/10 text-success' : summary.state === 'fail'
              ? 'border-danger/30 bg-danger/10 text-danger' : 'border-warning/30 bg-warning/10 text-warning'}`}>
            {summary.text}
          </div>
          <Table>
            <Thead><Tr><Th>Оператор</Th><Th>Результат</Th><Th>Адрес телефона</Th></Tr></Thead>
            <Tbody>
              {results.map((r) => (
                <Tr key={r.ip}>
                  <Td>{r.operator ? <span className="text-primary">{r.operator}</span> : <span className="text-tertiary">не определён</span>}</Td>
                  <Td><Badge variant={r.ok ? 'success' : 'danger'}>{r.verdict}</Badge></Td>
                  <Td mono className="text-secondary">{r.ip}</Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        </>
      )}

      {check?.active && (
        <p className="flex items-center gap-1 text-[11px] text-tertiary">
          <RefreshCw className="w-3 h-3" aria-hidden="true" /> Обновляется каждые 3 секунды. Страница выключится сама через 30 минут.
        </p>
      )}
    </div>
  )
}
