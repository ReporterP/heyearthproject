import { useEffect, useMemo, useState } from 'react'
import { RefreshCw, Send, TriangleAlert } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { useUrlState } from '../hooks/useUrlState.js'
import { fetchAlertSettings, saveAlertSettings, sendAlertTest, fetchAlertEvents } from '../api/admin.js'
import { formatDate } from '../utils/format.js'
import {
  Badge, Button, Card, CardHeader, CardBody, EmptyState, Input, PageHeader, Spinner,
  Table, Thead, Tbody, Tr, Th, Td,
} from '../components/ui/index.js'

const KIND = {
  raise:      { label: 'Тревога',       variant: 'danger' },
  repeat:     { label: 'Повтор',        variant: 'warning' },
  resolve:    { label: 'Восстановлено', variant: 'success' },
  suppressed: { label: 'Подавлено',     variant: 'default' },
  cleared:    { label: 'Снято',         variant: 'default' },
  test:       { label: 'Тест',          variant: 'info' },
}

// 1 алерт, 2 алерта, 5 алертов
function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100
  if (m10 === 1 && m100 !== 11) return one
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few
  return many
}

function undeliveredHeadline(n, reason) {
  return `${n} ${plural(n, 'алерт', 'алерта', 'алертов')} ${plural(n, 'не доставлен', 'не доставлено', 'не доставлено')}: ${reason}`
}

const isFailedAttempt = ev => (ev.kind === 'raise' || ev.kind === 'repeat') && !ev.delivered

// Collapses runs of identical failed delivery attempts of one key (same error, same level) into
// the newest row with a counter. Events come newest first; any other row of the key ends the run.
function collapseRepeats(events) {
  const open = new Map()
  const rows = []
  let hidden = 0
  for (const ev of events) {
    const run = open.get(ev.key)
    if (isFailedAttempt(ev) && run && run.error === ev.error && run.level === ev.level) {
      run.count += 1
      run.firstAt = ev.created_at
      hidden += 1
      continue
    }
    const row = { ...ev, count: 1, firstAt: ev.created_at }
    if (isFailedAttempt(ev)) open.set(ev.key, row)
    else open.delete(ev.key)
    rows.push(row)
  }
  return { rows, hidden }
}

function LevelBadge({ ev }) {
  if (ev.kind === 'test') return <Badge variant="default">инфо</Badge>
  return ev.level === 'critical'
    ? <Badge variant="danger">критично</Badge>
    : <Badge variant="warning">внимание</Badge>
}

function Delivery({ ev }) {
  if (ev.kind === 'suppressed' || ev.kind === 'cleared') return <span className="text-tertiary">—</span>
  if (ev.delivered) return <span className="text-success">отправлено</span>
  const range = ev.count > 1 ? `${ev.count} попыток подряд, первая: ${formatDate(ev.firstAt)}` : ''
  return (
    <span className="flex items-center gap-1.5 min-w-0 max-w-full" title={[ev.error, range].filter(Boolean).join('\n') || undefined}>
      <span className="text-danger shrink-0">не доставлено</span>
      {ev.count > 1 && <span className="shrink-0 font-mono tabular-nums text-tertiary">×{ev.count}</span>}
      {ev.error && <span className="min-w-0 truncate text-secondary">· {ev.error}</span>}
    </span>
  )
}

function UndeliveredBanner({ items }) {
  if (items.length === 0) return null
  const notConfigured = items.filter(u => u.not_configured)
  const failing = items.filter(u => !u.not_configured)
  return (
    <div className="space-y-2">
      {notConfigured.length > 0 && (
        <div role="status" className="rounded border border-warning/30 bg-warning/10 px-3 py-2">
          <div className="flex items-start gap-2">
            <TriangleAlert className="w-4 h-4 mt-0.5 shrink-0 text-warning" aria-hidden="true" />
            <div className="min-w-0 space-y-1">
              <p className="text-sm font-medium text-warning">{undeliveredHeadline(notConfigured.length, 'Telegram не настроен')}</p>
              <p className="text-xs text-secondary">
                Сбои фиксируются, но никто о них не узнаёт. Заполните токен бота и Chat ID ниже — после сохранения тревоги уйдут при следующей проверке.
              </p>
              <UndeliveredList items={notConfigured} />
            </div>
          </div>
        </div>
      )}
      {failing.length > 0 && (
        <div role="alert" className="rounded border border-danger/30 bg-danger/10 px-3 py-2">
          <div className="flex items-start gap-2">
            <TriangleAlert className="w-4 h-4 mt-0.5 shrink-0 text-danger" aria-hidden="true" />
            <div className="min-w-0 space-y-1">
              <p className="text-sm font-medium text-danger">{undeliveredHeadline(failing.length, 'ошибка отправки в Telegram')}</p>
              <p className="text-xs text-secondary break-words">Последняя ошибка: {failing[0].error || '—'}</p>
              <UndeliveredList items={failing} />
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function UndeliveredList({ items }) {
  return (
    <ul className="text-xs text-secondary space-y-0.5">
      {items.map(u => (
        <li key={u.key} className="flex flex-wrap items-baseline gap-x-2">
          <span className="font-mono text-primary">{u.key}</span>
          <span className={u.level === 'critical' ? 'text-danger' : 'text-warning'}>{u.level === 'critical' ? 'критично' : 'внимание'}</span>
          <span className="min-w-0 max-w-full sm:max-w-sm truncate">{u.title}</span>
          <span className="text-tertiary">
            попыток: <span className="font-mono tabular-nums">{u.attempts}</span>, последняя <span className="font-mono tabular-nums">{formatDate(u.last_attempt_at)}</span>
          </span>
        </li>
      ))}
    </ul>
  )
}

export default function AlertsPage() {
  const { can } = useAuth()
  const toast = useToast()
  const canWrite = can('settings.write')
  const [view, setView] = useUrlState({ repeats: false })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [cfg, setCfg] = useState({ token_set: false, token_hint: '', chat_id: '' })
  const [token, setToken] = useState('')
  const [chatId, setChatId] = useState('')
  const [events, setEvents] = useState([])
  const [undelivered, setUndelivered] = useState([])
  const [settingsError, setSettingsError] = useState('')
  const [eventsError, setEventsError] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)

  // Until the stored settings are loaded the form stays locked: saving the defaults
  // (empty chat_id) would overwrite what is really configured.
  const formLocked = !canWrite || !!settingsError

  const shown = useMemo(
    () => (view.repeats ? { rows: events.map(ev => ({ ...ev, count: 1 })), hidden: 0 } : collapseRepeats(events)),
    [events, view.repeats],
  )

  async function loadEvents() {
    try {
      const data = await fetchAlertEvents(100)
      setEvents(data.events || [])
      setUndelivered(data.undelivered || [])
      setEventsError('')
    } catch (e) { setEventsError(e.message) }
  }

  async function loadSettings() {
    try {
      const s = await fetchAlertSettings()
      setCfg(s)
      setChatId(s.chat_id || '')
      setSettingsError('')
    } catch (e) { setSettingsError(e.message) }
  }

  async function load() {
    setLoading(true)
    await Promise.all([loadSettings(), loadEvents()])
    setLoading(false)
  }

  useEffect(() => { load() }, [])

  async function save(clearToken) {
    setSaving(true)
    try {
      await saveAlertSettings({ bot_token: clearToken ? '' : token, chat_id: chatId, clear_token: clearToken })
      setToken('')
      setConfirmDelete(false)
      toast.success(clearToken ? 'Токен удалён' : 'Настройки алертов сохранены')
      await loadSettings()
    } catch (e) { toast.error(e.message) } finally { setSaving(false) }
  }

  async function test() {
    setTesting(true)
    try {
      await sendAlertTest()
      toast.success('Тестовое сообщение отправлено')
    } catch (e) { toast.error(e.message) } finally {
      setTesting(false)
      loadEvents()
    }
  }

  if (loading) return <div className="flex justify-center py-20"><Spinner size="md" /></div>

  return (
    <div className="max-w-7xl space-y-6">
      <PageHeader
        title="Алерты"
        subtitle="Доступность входа (снаружи и из РФ), Бамбук, exit-ноды, отключение по квоте, xray, база данных, сертификат, бэкапы и диск — сообщения в Telegram-чат администратора."
      />

      <UndeliveredBanner items={undelivered} />

      <Card className="max-w-2xl">
        <CardHeader><h3 className="text-sm font-semibold text-primary">Telegram</h3></CardHeader>
        <CardBody className="space-y-4">
          {settingsError && (
            <div role="alert" className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
              <div>
                <p className="text-sm font-medium text-danger">Не удалось загрузить настройки</p>
                <p className="text-xs text-secondary mt-0.5">{settingsError}. Форма заблокирована, чтобы не затереть сохранённые значения.</p>
              </div>
              <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Повторить</Button>
            </div>
          )}
          <div>
            <label htmlFor="alert-bot-token" className="text-xs text-tertiary block mb-1">Токен бота</label>
            <Input id="alert-bot-token" type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)}
              placeholder={cfg.token_set ? `Сохранён: ${cfg.token_hint} — введите новый, чтобы заменить` : '123456789:AA…'}
              aria-describedby="alert-bot-token-hint" className="font-mono" disabled={formLocked} />
            <p id="alert-bot-token-hint" className="text-xs text-tertiary mt-1">Создайте бота в @BotFather. Токен хранится на сервере и в админку не возвращается.</p>
          </div>
          <div>
            <label htmlFor="alert-chat-id" className="text-xs text-tertiary block mb-1">Chat ID</label>
            <Input id="alert-chat-id" value={chatId} onChange={e => setChatId(e.target.value)} placeholder="-1001234567890"
              aria-describedby="alert-chat-id-hint" className="font-mono" disabled={formLocked} />
            <p id="alert-chat-id-hint" className="text-xs text-tertiary mt-1">ID чата или канала (или @channel). Добавьте бота в чат и отправьте ему /start.</p>
          </div>
          {canWrite && (
            <div className="flex flex-wrap gap-2 pt-1">
              <Button onClick={() => save(false)} loading={saving} disabled={formLocked}>Сохранить</Button>
              <Button variant="secondary" icon={<Send className="w-3.5 h-3.5" />} onClick={test} loading={testing}
                disabled={formLocked || !cfg.token_set || !cfg.chat_id}>Отправить тест</Button>
              {cfg.token_set && !confirmDelete && (
                <Button variant="ghost" onClick={() => setConfirmDelete(true)} disabled={formLocked || saving}>Удалить токен</Button>
              )}
            </div>
          )}
          {canWrite && confirmDelete && (
            <div role="alert" className="flex flex-wrap items-center gap-2 rounded border border-danger/30 bg-danger/10 px-3 py-2">
              <span className="basis-full text-xs text-primary">Удалить токен бота? Сообщения о сбоях перестанут приходить в Telegram.</span>
              <Button variant="danger" size="sm" onClick={() => save(true)} loading={saving}>Да, удалить</Button>
              <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(false)} disabled={saving}>Отмена</Button>
            </div>
          )}
        </CardBody>
      </Card>

      <div className="space-y-2">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
          <h3 className="text-sm font-semibold text-primary">Журнал событий</h3>
          <div className="flex flex-wrap items-center gap-3">
            <label htmlFor="alerts-show-repeats" className="inline-flex items-center gap-2 text-xs text-secondary cursor-pointer select-none">
              <input id="alerts-show-repeats" type="checkbox" className="h-3.5 w-3.5 accent-accent"
                checked={view.repeats} onChange={e => setView({ repeats: e.target.checked })} />
              Показывать повторы недоставленных
              {!view.repeats && shown.hidden > 0 && (
                <span className="text-tertiary">(скрыто <span className="font-mono tabular-nums">{shown.hidden}</span>)</span>
              )}
            </label>
            <Button variant="ghost" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={loadEvents}>Обновить</Button>
          </div>
        </div>
        {eventsError && (
          <div role="alert" className="text-xs text-danger">Не удалось загрузить журнал: {eventsError}</div>
        )}
        {events.length === 0 ? (
          eventsError ? null : (
            <Card><EmptyState title="Событий пока нет" description="Здесь появятся тревоги, восстановления и тестовые сообщения." /></Card>
          )
        ) : (
          <Table>
            <Thead>
              <Tr><Th>Время</Th><Th>Уровень</Th><Th>Событие</Th><Th>Ключ</Th><Th>Сообщение</Th><Th>Доставка</Th></Tr>
            </Thead>
            <Tbody>
              {shown.rows.map(ev => {
                const kind = KIND[ev.kind] || { label: ev.kind, variant: 'default' }
                return (
                  <Tr key={ev.id}>
                    <Td mono className="whitespace-nowrap text-secondary">{formatDate(ev.created_at)}</Td>
                    <Td><LevelBadge ev={ev} /></Td>
                    <Td><Badge variant={kind.variant}>{kind.label}</Badge></Td>
                    <Td mono className="text-secondary whitespace-nowrap">{ev.key}</Td>
                    <Td className="max-w-xs">
                      <span className="block truncate text-primary" title={ev.details}>
                        {ev.title}{ev.details ? <span className="text-tertiary"> — {ev.details}</span> : null}
                      </span>
                    </Td>
                    <Td className="max-w-[14rem]"><Delivery ev={ev} /></Td>
                  </Tr>
                )
              })}
            </Tbody>
          </Table>
        )}
      </div>
    </div>
  )
}
