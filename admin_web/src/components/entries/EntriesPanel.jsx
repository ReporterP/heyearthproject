import { useEffect, useState } from 'react'
import { CheckCircle2, Plus, RefreshCw, Smartphone, TriangleAlert } from 'lucide-react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { fetchEntries, fetchEntriesPreview, saveEntryAddress, saveEntrySettings, saveEntryMode } from '../../api/admin.js'
import { formatDate } from '../../utils/format.js'
import {
  Badge, Button, Card, CardHeader, CardBody, Dialog, Input, Select, Skeleton,
  Table, Thead, Tbody, Tr, Th, Td,
} from '../ui/index.js'
import { confirmMatches } from '../ui/confirm.js' // the typed-confirmation rule of ConfirmDanger (phase 2)
import MobileCheckPanel from './MobileCheckPanel.jsx'
import HubKeyBox from './HubKeyBox.jsx'
import { ENTRY_MODES, healthyRelays, modeBanner } from './entriesModel.js'
import { loadView } from '../servers/serversModel.js'

const STATUS = {
  healthy: ['доступен', 'success'], reachable: ['доступен', 'success'],
  unhealthy: ['недоступен', 'danger'], unreachable: ['недоступен', 'danger'],
  unknown: ['нет данных', 'default'],
}
const DRILL_WORD = 'ПРОВЕРКА'
const EVENT = { raise: ['тревога', 'danger'], resolve: ['восстановлено', 'success'] }
const TONE = {
  success: 'border-success/30 bg-success/10 text-success',
  danger: 'border-danger/30 bg-danger/10 text-danger',
  default: 'border-default bg-elevated text-secondary',
}

function Health({ status, at, detail, link }) {
  const [label, variant] = STATUS[status] || STATUS.unknown
  const when = at ? formatDate(at) : '—'
  return (
    <div className="flex flex-col items-start gap-0.5 min-w-0" title={detail || undefined}>
      <Badge variant={variant}>{label}</Badge>
      {link
        ? <a href={link} target="_blank" rel="noreferrer" className="text-[11px] font-mono tabular-nums text-tertiary hover:text-accent">{when}</a>
        : <span className="text-[11px] font-mono tabular-nums text-tertiary">{when}</span>}
    </div>
  )
}

function Lines({ title, lines }) {
  return (
    <div className="min-w-0">
      <h4 className="text-xs font-medium text-tertiary mb-1">{title}</h4>
      <ul className="space-y-0.5">
        {lines.map(l => (
          <li key={l.label} className="flex items-baseline justify-between gap-3 text-sm">
            <span className={`truncate ${l.label.includes('резерв') ? 'text-accent' : 'text-primary'}`}>{l.label}</span>
            <span className="shrink-0 font-mono tabular-nums text-xs text-secondary">{l.address}:{l.port}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

// SetupGuide: what to do when there is no reserve entry yet — the owner's whole path in four steps.
function SetupGuide({ canEdit, onStart }) {
  const steps = [
    ['Купите сервер в России у другого провайдера', 'Не там, где Hub. Подходит самый дешёвый облачный сервер: Ubuntu 24.04, 1 vCPU, 1 ГБ, публичный IPv4, безлимитный трафик (например, Timeweb Cloud). Платите почасово, пока адрес не проверен.'],
    ['При создании вставьте ключ Hub', 'В поле «SSH-ключ» для root — тогда пароль вводить не нужно.'],
    ['Добавьте сервер и проверьте адрес с телефона', 'Мастер покажет QR-код: откройте его на телефоне без Wi-Fi. Если адрес не проходит — смените IP у хостера.'],
    ['Настройте и включите для всех', 'Мастер сам настроит пересылку на Hub, проверит вход снаружи и одной кнопкой переведёт на него подписку пользователей.'],
  ]
  return (
    <Card>
      <CardHeader>
        <h3 className="flex items-center gap-2 text-sm font-semibold text-primary">
          <Smartphone className="w-4 h-4 text-accent" aria-hidden="true" /> Мобильный интернет: как подключить вход
        </h3>
      </CardHeader>
      <CardBody className="space-y-4">
        <p className="text-sm text-secondary">
          Мобильные операторы режут соединения к адресам многих хостингов, в том числе к адресу Hub. Нужен второй адрес входа —
          сервер в России в подсети, которую операторы пропускают. Он пересылает подключения на Hub, своих данных не хранит.
        </p>
        <ol className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          {steps.map(([title, text], i) => (
            <li key={title} className="flex gap-3 rounded border border-default p-3">
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded bg-accent/10 font-mono text-xs text-accent">{i + 1}</span>
              <div className="min-w-0">
                <div className="text-sm text-primary">{title}</div>
                <div className="text-xs text-tertiary mt-0.5">{text}</div>
              </div>
            </li>
          ))}
        </ol>
        <HubKeyBox />
        {canEdit && <Button icon={<Plus className="w-3.5 h-3.5" />} onClick={onStart}>Добавить сервер</Button>}
      </CardBody>
    </Card>
  )
}

// ModeCard: «Куда подключаются пользователи» — the three modes with one line of meaning each.
function ModeCard({ data, canEdit, onSaved }) {
  const toast = useToast()
  const relays = healthyRelays(data.entries)
  const allRelays = data.entries.filter(e => e.kind === 'relay')
  const [mode, setMode] = useState(data.failover.mode)
  const [target, setTarget] = useState(data.failover.pinned ? data.failover.target : relays[0]?.id || 0)
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    setMode(data.failover.mode)
    setTarget(data.failover.pinned ? data.failover.target : relays[0]?.id || 0)
  }, [data.failover.mode, data.failover.target, data.failover.pinned])

  const changed = mode !== data.failover.mode || (mode === 'relay' && target !== data.failover.target)
  async function save() {
    setSaving(true)
    try {
      await saveEntryMode(mode, mode === 'relay' ? target : 0)
      toast.success('Режим сохранён — подписки пользователей обновятся автоматически')
      await onSaved()
    } catch (e) { toast.error(e.message) } finally { setSaving(false) }
  }

  return (
    <Card>
      <CardHeader><h3 className="text-sm font-semibold text-primary">Куда подключаются пользователи</h3></CardHeader>
      <CardBody className="space-y-3">
        <div role="radiogroup" aria-label="Режим входа" className="grid grid-cols-1 lg:grid-cols-3 gap-2">
          {ENTRY_MODES.map(m => {
            const disabled = !canEdit || (m.key === 'relay' && allRelays.length === 0)
            return (
              <label key={m.key} className={`flex gap-2 rounded border p-3 ${mode === m.key ? 'border-accent bg-accent/10' : 'border-default'} ${disabled ? 'opacity-50' : 'cursor-pointer hover:bg-hover'}`}>
                <input type="radio" name="entry-mode" className="mt-1 accent-accent" checked={mode === m.key} disabled={disabled}
                  onChange={() => setMode(m.key)} />
                <span className="min-w-0">
                  <span className="block text-sm text-primary">{m.title}{data.failover.mode === m.key && <Badge variant="accent" className="ml-2">сейчас</Badge>}</span>
                  <span className="block text-xs text-tertiary mt-1">{m.text}</span>
                </span>
              </label>
            )
          })}
        </div>
        {mode === 'relay' && (
          <div className="flex flex-col sm:flex-row sm:items-center gap-2">
            <label htmlFor="entry-target" className="text-xs text-secondary">Резервный вход</label>
            <Select id="entry-target" className="w-full sm:w-auto" value={String(target)} disabled={!canEdit}
              onChange={e => setTarget(Number(e.target.value))}>
              {allRelays.map(r => (
                <option key={r.id} value={r.id} disabled={r.health.external !== 'healthy'}>
                  {r.name} — {r.address}{r.health.external !== 'healthy' ? ' (сейчас не отвечает)' : ''}
                </option>
              ))}
            </Select>
          </div>
        )}
        {mode === 'relay' && relays.length === 0 && allRelays.length > 0 && (
          <p className="text-xs text-warning">Ни один резервный вход сейчас не отвечает снаружи — выбрать его можно, когда проверка станет зелёной.</p>
        )}
        {canEdit && (
          <Button loading={saving} disabled={!changed || (mode === 'relay' && !relays.some(r => r.id === target))} onClick={save}>Сохранить режим</Button>
        )}
      </CardBody>
    </Card>
  )
}

// The site's names that must point at the entries for the site and the subscription to open on
// mobile networks (the 443 router's names; relays pass them to the Hub with the client's address).
const SITE_NAMES = ['hiearth.ru', 'www.hiearth.ru', 'admin.hiearth.ru', 'site.hiearth.ru']

// DnsCard: the owner's DNS step once an entry works — the site and the subscription link live on
// the domain, and the domain still points at the Hub that mobile networks cut.
function DnsCard({ relays }) {
  const ips = relays.filter(r => r.health.external === 'healthy').map(r => r.address)
  return (
    <Card id="dns" className="scroll-mt-4">
      <CardHeader><h3 className="text-sm font-semibold text-primary">Сайт и подписка на мобильных</h3></CardHeader>
      <CardBody className="space-y-3">
        <p className="text-sm text-secondary">
          VPN через вход уже работает, но сайт (регистрация, оплата) и ссылка подписки открываются по домену, а он указывает на Hub,
          который мобильные операторы режут. У регистратора домена замените A-записи на адрес входа — вход передаст всё на Hub
          с настоящим IP посетителя (оплаты, лимиты и журнал продолжат работать).
        </p>
        {ips.length === 0 ? (
          <p className="text-xs text-warning">Сначала дождитесь, пока вход станет «доступен» снаружи.</p>
        ) : (
          <Table>
            <Thead><Tr><Th>Имя</Th><Th>Тип</Th><Th>Значение</Th><Th>TTL</Th></Tr></Thead>
            <Tbody>
              {SITE_NAMES.flatMap(n => ips.map(ip => (
                <Tr key={n + ip}><Td mono>{n}</Td><Td mono>A</Td><Td mono>{ip}</Td><Td mono>300</Td></Tr>
              )))}
            </Tbody>
          </Table>
        )}
        <p className="text-xs text-tertiary">
          Старую A-запись с адресом Hub удалите: иначе телефоны будут иногда попадать на него и «висеть». Изменение расходится по
          интернету от нескольких минут до пары часов.
        </p>
      </CardBody>
    </Card>
  )
}

// LoadCells: a relay's latest sample (the nodes list) in the entries table.
function LoadCells({ node }) {
  const v = loadView(node?.load, { portMbps: node?.owner?.port_mbps, role: 'relay' })
  if (v.empty) {
    return (
      <>
        <Td align="right"><span className="text-xs text-tertiary" title={v.why}>нет данных</span></Td>
        <Td align="right"><span className="text-tertiary">—</span></Td>
        <Td align="right"><span className="text-tertiary">—</span></Td>
      </>
    )
  }
  const tone = (c) => (c.warn ? 'text-warning' : v.stale ? 'text-tertiary' : 'text-primary')
  return (
    <>
      <Td mono align="right" className="whitespace-nowrap"><span className={tone(v.cpu)} title={v.why || undefined}>{v.cpu.text}</span></Td>
      <Td mono align="right" className="whitespace-nowrap"><span className={tone(v.net)}>{v.net.text}</span></Td>
      <Td mono align="right" className="whitespace-nowrap"><span className={tone(v.conntrack)}>{v.conntrack.text}</span></Td>
    </>
  )
}

/**
 * EntriesPanel — the «Входы в РФ» tab of «Серверы» (was the «Входы» page): where users connect
 * (hub / pinned relay / auto), the DNS step, every entry's health and load, the phone check, the
 * subscription preview, settings, the drill and the history.
 * Props: nodesById (GET /nodes items by id — the load), onAdd (the wizard), onOpenNode(id),
 * phoneId / onPhone(id|null) (the phone check dialog lives in the URL: ?phone=<id>).
 */
export default function EntriesPanel({ nodesById = {}, onAdd, onOpenNode, phoneId, onPhone }) {
  const { can } = useAuth()
  const toast = useToast()
  // The page is servers.read; every change here (mode, address mode, settings, drill) needs nodes.provision.
  const canEdit = can('nodes.provision')
  const [data, setData] = useState(null)
  const [preview, setPreview] = useState({ now: [], failover: [] })
  const [error, setError] = useState('')
  const [form, setForm] = useState({ interval: 4, testUser: '' })
  const [saving, setSaving] = useState(false)
  const [drill, setDrill] = useState({ open: false, minutes: '15', word: '' })

  async function load() {
    try {
      const [d, p] = await Promise.all([fetchEntries(), fetchEntriesPreview()])
      setData(d)
      setPreview(p)
      setForm({ interval: d.update_interval_hours, testUser: d.failover.test_user || '' })
      setError('')
    } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])
  // The monitor checks every entry about every two minutes: keep the page current while it is open.
  useEffect(() => {
    const t = setInterval(() => { load() }, 60000)
    return () => clearInterval(t)
  }, [])

  async function save(patch, ok) {
    setSaving(true)
    try {
      await saveEntrySettings(patch)
      toast.success(ok)
      setDrill({ open: false, minutes: '15', word: '' })
      await load()
    } catch (e) { toast.error(e.message) } finally { setSaving(false) }
  }

  async function setAddressMode(entry, mode) {
    try {
      await saveEntryAddress(entry.id, { address_mode: mode })
      toast.success('Адрес входа сохранён')
      await load()
    } catch (e) { toast.error(e.message) }
  }

  if (!data && !error) return <div className="space-y-3"><Skeleton className="h-10" /><Skeleton className="h-32" /><Skeleton className="h-40" /></div>
  if (!data) return (
    <div role="alert" className="flex items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
      <span className="text-sm text-danger">Не удалось загрузить входы: {error}</span>
      <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Повторить</Button>
    </div>
  )

  const relays = data.entries.filter(e => e.kind === 'relay')
  const phone = phoneId ? relays.find(e => String(e.id) === String(phoneId)) : null
  const banner = modeBanner(data.failover, data.entries)
  const BannerIcon = banner.tone === 'danger' ? TriangleAlert : CheckCircle2
  return (
    <div className="space-y-6">
      <p className="text-xs text-tertiary">
        Адреса, к которым подключаются пользователи: Hub и входы в РФ для мобильных сетей. Доступность проверяется каждые две минуты
        снаружи (через выход за рубежом) и раз в 15 минут с российских узлов check-host.
      </p>

      <div role="status" className={`flex items-start gap-2 rounded border px-3 py-2 text-sm ${TONE[banner.tone]}`}>
        <BannerIcon className="w-4 h-4 mt-0.5 shrink-0" aria-hidden="true" />
        <span className="min-w-0">{banner.text}</span>
      </div>

      {relays.length === 0 && <SetupGuide canEdit={canEdit} onStart={onAdd} />}

      <ModeCard data={data} canEdit={canEdit} onSaved={load} />

      {relays.length > 0 && <DnsCard relays={relays} />}

      <Table>
        <Thead><Tr><Th>Вход</Th><Th>Адрес</Th><Th>Снаружи</Th><Th>Бамбук через вход</Th><Th>Из РФ</Th><Th align="right">CPU</Th><Th align="right">Канал, Мбит/с</Th><Th align="right">conntrack</Th><Th>Адрес в подписке</Th><Th>Мобильные сети</Th></Tr></Thead>
        <Tbody>
          {data.entries.map(e => (
            <Tr key={e.id}>
              <Td>
                {e.kind === 'relay' && onOpenNode
                  ? <button type="button" onClick={() => onOpenNode(e.id)} className="text-left text-primary hover:text-accent rounded focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent">{e.name}</button>
                  : <span className="text-primary">{e.name}</span>}
                {e.kind === 'relay' && <span className="block text-[11px] text-tertiary">вход в РФ</span>}
              </Td>
              <Td mono className="text-secondary">{e.address || '—'}</Td>
              <Td><Health status={e.health.external} at={e.health.external_at} detail={e.health.external_error} /></Td>
              <Td>{e.kind === 'relay' ? <Health status={e.health.udp} at={e.health.udp_at} detail={e.health.udp_error} /> : <span className="text-tertiary">см. «Алерты»</span>}</Td>
              <Td><Health status={e.health.ru} at={e.health.ru_at} link={e.health.ru_link} /></Td>
              {e.kind === 'relay'
                ? <LoadCells node={nodesById[e.id]} />
                : <><Td align="right"><span className="text-xs text-tertiary">вкладка «Hub»</span></Td><Td /><Td /></>}
              <Td>
                <Select aria-label={`Адрес входа ${e.name}`} value={e.address_mode} disabled={!canEdit}
                  onChange={ev => setAddressMode(e, ev.target.value)} className="h-7 text-xs">
                  <option value="ip">IP-адрес</option>
                  <option value="domain" disabled={!data.domain_mode_available}>домен{data.domain_mode_available ? '' : ' — после раздела «Домены»'}</option>
                </Select>
              </Td>
              <Td>
                {e.kind === 'relay'
                  ? <Button size="sm" variant="secondary" icon={<Smartphone className="w-3.5 h-3.5" />} onClick={() => onPhone?.(e.id)}>Проверить с телефона</Button>
                  : <span className="text-xs text-tertiary">режется операторами</span>}
              </Td>
            </Tr>
          ))}
        </Tbody>
      </Table>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><h3 className="text-sm font-semibold text-primary">Строки подписки (образцовый пользователь со всеми протоколами)</h3></CardHeader>
          <CardBody className="grid gap-4 sm:grid-cols-2">
            <Lines title="Сейчас" lines={preview.now} />
            <Lines title="При аварии основного входа" lines={preview.failover} />
          </CardBody>
        </Card>
        <Card>
          <CardHeader><h3 className="text-sm font-semibold text-primary">Настройки</h3></CardHeader>
          <CardBody className="space-y-4">
            <div>
              <label htmlFor="entries-interval" className="text-xs text-tertiary block mb-1">Как часто приложения обновляют подписку, часов</label>
              <Input id="entries-interval" type="number" min={1} max={24} className="w-24 font-mono tabular-nums" disabled={!canEdit}
                value={form.interval} onChange={e => setForm({ ...form, interval: e.target.value })} />
              <p className="text-xs text-tertiary mt-1">Во время аварии — всегда раз в час.</p>
            </div>
            <div>
              <label htmlFor="entries-test-user" className="text-xs text-tertiary block mb-1">Тестовый пользователь (видит подписку как при аварии)</label>
              <Input id="entries-test-user" className="font-mono" placeholder="ID из «Пользователей»" disabled={!canEdit}
                value={form.testUser} onChange={e => setForm({ ...form, testUser: e.target.value })} />
              <p className="text-xs text-tertiary mt-1">Удобно проверить новый вход на своём телефоне, прежде чем переводить на него всех.</p>
            </div>
            {canEdit && (
              <Button loading={saving} onClick={() => save({ update_interval_hours: Number(form.interval), test_user: form.testUser.trim() }, 'Настройки входов сохранены')}>Сохранить</Button>
            )}
          </CardBody>
        </Card>
      </div>

      {canEdit && (
        <Card>
          <CardHeader><h3 className="text-sm font-semibold text-primary">Учебная авария</h3></CardHeader>
          <CardBody className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
            <p className="text-xs text-secondary">
              {data.failover.simulate_until
                ? <>Идёт до <span className="font-mono tabular-nums">{formatDate(data.failover.simulate_until)}</span>: монитор считает основной вход недоступным.</>
                : 'Монитор на время будет считать Hub недоступным. В режиме «Hub, а при аварии — резервный вход» основные строки подписки переключатся на здоровый вход у ВСЕХ пользователей.'}
            </p>
            {data.failover.simulate_until
              ? <Button variant="secondary" loading={saving} onClick={() => save({ simulate_hub_fail_minutes: 0 }, 'Учебная авария остановлена')}>Остановить</Button>
              : <Button variant="danger" onClick={() => setDrill({ ...drill, open: true })}>Начать…</Button>}
          </CardBody>
        </Card>
      )}

      <Dialog open={drill.open} onClose={() => setDrill({ ...drill, open: false })} title="Учебная авария основного входа" maxWidth="max-w-md">
        <div className="space-y-3 text-sm">
          <p className="text-secondary">Через ~3 минуты основные строки подписки у всех пользователей переключатся на резервный вход и вернутся через 30 минут после окончания учений. Каждая подмена придёт алертом в Telegram.</p>
          <Select aria-label="Длительность" value={drill.minutes} onChange={e => setDrill({ ...drill, minutes: e.target.value })}>
            {['10', '15', '30', '60'].map(m => <option key={m} value={m}>{m} минут</option>)}
          </Select>
          <label htmlFor="drill-word" className="text-xs text-tertiary block">Введите {DRILL_WORD}, чтобы начать</label>
          <Input id="drill-word" value={drill.word} onChange={e => setDrill({ ...drill, word: e.target.value })} />
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setDrill({ ...drill, open: false })}>Отмена</Button>
            <Button variant="danger" loading={saving} disabled={!confirmMatches(drill.word, DRILL_WORD)}
              onClick={() => save({ simulate_hub_fail_minutes: Number(drill.minutes) }, 'Учебная авария начата')}>Начать</Button>
          </div>
        </div>
      </Dialog>

      <Dialog open={!!phone} onClose={() => onPhone?.(null)} title={phone ? `Проверка с телефона — ${phone.name}` : ''} maxWidth="max-w-2xl">
        {phone && <MobileCheckPanel serverId={phone.id} canStart={canEdit} />}
      </Dialog>

      <div className="space-y-2">
        <h3 className="text-sm font-semibold text-primary">История входов</h3>
        {data.history.length === 0 ? <p className="text-xs text-tertiary">Событий пока нет: тревоги и восстановления входов появятся здесь.</p> : (
          <Table>
            <Thead><Tr><Th>Время</Th><Th>Событие</Th><Th>Что случилось</Th></Tr></Thead>
            <Tbody>
              {data.history.map(ev => {
                const [label, variant] = EVENT[ev.kind] || [ev.kind, 'default']
                return (
                  <Tr key={ev.id}>
                    <Td mono className="whitespace-nowrap text-secondary">{formatDate(ev.created_at)}</Td>
                    <Td><Badge variant={variant}>{label}</Badge></Td>
                    <Td className="max-w-md"><span className="block truncate text-primary" title={ev.details}>{ev.title}{ev.details ? <span className="text-tertiary"> — {ev.details}</span> : null}</span></Td>
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
