import { useEffect, useState } from 'react'
import { RefreshCw, Send } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
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

function Delivery({ ev }) {
  if (ev.kind === 'suppressed' || ev.kind === 'cleared') return <span className="text-tertiary">—</span>
  if (ev.delivered) return <span className="text-success">отправлено</span>
  return <span className="text-danger" title={ev.error}>{ev.error ? 'ошибка' : 'не отправлено'}</span>
}

export default function AlertsPage() {
  const { can } = useAuth()
  const toast = useToast()
  const canWrite = can('settings.write')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [cfg, setCfg] = useState({ token_set: false, token_hint: '', chat_id: '' })
  const [token, setToken] = useState('')
  const [chatId, setChatId] = useState('')
  const [events, setEvents] = useState([])
  const [settingsError, setSettingsError] = useState('')
  const [eventsError, setEventsError] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)

  // Until the stored settings are loaded the form stays locked: saving the defaults
  // (empty chat_id) would overwrite what is really configured.
  const formLocked = !canWrite || !!settingsError

  async function loadEvents() {
    try {
      const data = await fetchAlertEvents(100)
      setEvents(data.events || [])
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
    <div className="max-w-5xl space-y-6">
      <PageHeader title="Алерты" subtitle="Сбои входа, шторм рестартов, сертификат, бэкапы и диск — сообщения в Telegram-чат администратора." />

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
            <label className="text-xs text-tertiary block mb-1">Токен бота</label>
            <Input type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)}
              placeholder={cfg.token_set ? `Сохранён: ${cfg.token_hint} — введите новый, чтобы заменить` : '123456789:AA…'}
              className="font-mono" disabled={formLocked} />
            <p className="text-xs text-tertiary mt-1">Создайте бота в @BotFather. Токен хранится на сервере и в админку не возвращается.</p>
          </div>
          <div>
            <label className="text-xs text-tertiary block mb-1">Chat ID</label>
            <Input value={chatId} onChange={e => setChatId(e.target.value)} placeholder="-1001234567890"
              className="font-mono" disabled={formLocked} />
            <p className="text-xs text-tertiary mt-1">ID чата или канала (или @channel). Добавьте бота в чат и отправьте ему /start.</p>
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
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-primary">Журнал событий</h3>
          <Button variant="ghost" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={loadEvents}>Обновить</Button>
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
              {events.map(ev => {
                const kind = KIND[ev.kind] || { label: ev.kind, variant: 'default' }
                return (
                  <Tr key={ev.id}>
                    <Td mono className="whitespace-nowrap text-secondary">{formatDate(ev.created_at)}</Td>
                    <Td><Badge variant={ev.level === 'critical' ? 'danger' : 'warning'}>{ev.level === 'critical' ? 'критично' : 'внимание'}</Badge></Td>
                    <Td><Badge variant={kind.variant}>{kind.label}</Badge></Td>
                    <Td mono className="text-secondary whitespace-nowrap">{ev.key}</Td>
                    <Td className="max-w-md">
                      <span className="block truncate text-primary" title={ev.details}>
                        {ev.title}{ev.details ? <span className="text-tertiary"> — {ev.details}</span> : null}
                      </span>
                    </Td>
                    <Td><Delivery ev={ev} /></Td>
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
