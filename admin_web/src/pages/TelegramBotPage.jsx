import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { CheckCircle2, RefreshCw, Send, TriangleAlert } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { checkTelegramBot, checkTelegramChannel, fetchStars, fetchTelegramSettings, refundStars, saveStarsRate, saveTelegramSettings } from '../api/admin.js'
import { formatDate } from '../utils/format.js'
import { Badge, Button, Card, CardBody, CardHeader, CopyButton, Input, PageHeader, Spinner, Table, Thead, Tbody, Tr, Th, Td } from '../components/ui/index.js'

// «Telegram-бот» (plan TG7): the users' bot (token write-only, Mini App), the news channel and the
// owner's alerts bot side by side, with the steps the owner does in Telegram.
const STEPS = [
  ['Создайте бота', 'В Telegram откройте @BotFather → /newbot → имя «HiEarth» → username, например hiearth_app_bot. Это отдельный бот — не бот алертов.'],
  ['Вставьте токен', 'Скопируйте токен из @BotFather в поле ниже и нажмите «Сохранить». Сервер проверит его и сам поставит меню команд, кнопку «Кабинет» и описание.'],
  ['Проверьте', 'Откройте бота, нажмите «Запустить» и «Кабинет» — откроется личный кабинет. Привязать аккаунт сайта: hiearth.ru → «Настройки» → «Подключить Telegram».'],
  ['Создайте канал', 'Новый канал в Telegram, публичный, с @именем — для новостей, советов и акций.'],
  ['Добавьте бота в канал', 'Канал → Администраторы → добавить бота с правами «Публикация сообщений», «Редактирование» и «Удаление».'],
  ['Укажите канал', 'Впишите @имя канала ниже, сохраните и нажмите «Проверить канал».'],
]

function Result({ r }) {
  if (!r) return null
  return r.ok ? (
    <div role="status" className="flex items-start gap-2 rounded border border-success/30 bg-success/10 px-3 py-2 text-sm">
      <CheckCircle2 className="w-4 h-4 mt-0.5 shrink-0 text-success" aria-hidden="true" />
      <span className="text-primary">{r.text}</span>
    </div>
  ) : (
    <div role="alert" className="flex items-start gap-2 rounded border border-danger/30 bg-danger/10 px-3 py-2 text-sm">
      <TriangleAlert className="w-4 h-4 mt-0.5 shrink-0 text-danger" aria-hidden="true" />
      <span className="text-primary break-words">{r.text}</span>
    </div>
  )
}

const STARS_STATUS = {
  paid: { label: 'оплачен', variant: 'warning' },
  activated: { label: 'подписка включена', variant: 'success' },
  refunded: { label: 'звёзды возвращены', variant: 'default' },
  failed: { label: 'ошибка', variant: 'danger' },
}

// Paying in Stars: Telegram's terms (6.2) allow only Telegram Stars for digital goods inside the
// bot and the Mini App, so this is the only way to pay there.
function StarsCard({ canWrite, canRefund }) {
  const toast = useToast()
  const [d, setD] = useState(null)
  const [rate, setRate] = useState('')
  const [err, setErr] = useState('')
  const [saving, setSaving] = useState(false)
  const [refunding, setRefunding] = useState(0)
  const [confirm, setConfirm] = useState(0)

  async function load() {
    try { const r = await fetchStars(); setD(r); setRate(r.rate || ''); setErr('') } catch (e) { setErr(e.message) }
  }
  useEffect(() => { load() }, [])

  async function save() {
    setSaving(true)
    try { const r = await saveStarsRate(rate.trim()); setD(r); toast.success(r.enabled ? 'Курс сохранён — оплата звёздами включена' : 'Оплата звёздами выключена') }
    catch (e) { toast.error(e.message) } finally { setSaving(false) }
  }

  async function refund(id) {
    setRefunding(id)
    try { await refundStars(id); toast.success('Звёзды возвращены'); setConfirm(0); load() }
    catch (e) { toast.error(e.message) } finally { setRefunding(0) }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold text-primary">Оплата звёздами</h3>
          {d && (d.enabled ? <Badge variant="success">включена</Badge> : <Badge variant="warning">выключена</Badge>)}
        </div>
      </CardHeader>
      <CardBody className="space-y-4">
        <p className="text-xs text-secondary">Правила Telegram: внутри бота и Mini App подписку можно продавать только за звёзды. Ссылок на оплату на сайте там нет — на сайте остаётся ЮKassa. Один платёж — один срок тарифа, дни добавляются к текущей подписке.</p>
        {err && <p role="alert" className="text-sm text-danger">{err}</p>}
        <div>
          <label htmlFor="stars-rate" className="text-xs text-tertiary block mb-1">Курс: рублей за 1 ⭐</label>
          <div className="flex flex-col sm:flex-row gap-2">
            <Input id="stars-rate" value={rate} onChange={e => setRate(e.target.value)} placeholder="например 1.3" inputMode="decimal" className="font-mono sm:w-40" disabled={!canWrite} />
            {canWrite && <Button onClick={save} loading={saving}>Сохранить</Button>}
          </div>
          <p className="text-xs text-tertiary mt-1">Цена в звёздах = цена тарифа / курс, с округлением вверх. Пусто или 0 — оплата звёздами выключена.</p>
        </div>
        {d?.plans?.length > 0 && (
          <dl className="grid grid-cols-[minmax(0,1fr)_auto_auto] gap-x-4 gap-y-1 text-sm">
            {d.plans.map(p => (
              <div key={p.name} className="contents">
                <dt className="text-secondary truncate">{p.name}</dt>
                <dd className="font-mono tabular-nums text-tertiary text-right">{p.price_rub} ₽</dd>
                <dd className="font-mono tabular-nums text-primary text-right">{p.stars ? `${p.stars} ⭐` : '—'}</dd>
              </div>
            ))}
          </dl>
        )}
        {d?.payments?.length > 0 && (
          <Table>
            <Thead><Tr><Th>Когда</Th><Th>Пользователь</Th><Th>Тариф</Th><Th align="right">⭐</Th><Th>Статус</Th><Th align="right" /></Tr></Thead>
            <Tbody>
              {d.payments.map(p => {
                const st = STARS_STATUS[p.status] || STARS_STATUS.paid
                return (
                  <Tr key={p.id}>
                    <Td mono>{formatDate(p.created_at)}</Td>
                    <Td mono>{p.user_id ? `${p.user_id.slice(0, 4)}…${p.user_id.slice(-4)}` : p.telegram_id}</Td>
                    <Td>{p.plan_name || '—'}</Td>
                    <Td mono align="right">{p.stars}</Td>
                    <Td><Badge variant={st.variant}>{st.label}</Badge>{p.error && <p className="text-xs text-danger max-w-[220px] truncate" title={p.error}>{p.error}</p>}</Td>
                    <Td align="right">
                      {canRefund && p.status !== 'refunded' && (confirm === p.id
                        ? <span className="inline-flex gap-1"><Button variant="danger" size="sm" onClick={() => refund(p.id)} loading={refunding === p.id}>Вернуть</Button><Button variant="ghost" size="sm" onClick={() => setConfirm(0)}>Нет</Button></span>
                        : <Button variant="ghost" size="sm" onClick={() => setConfirm(p.id)}>Вернуть звёзды</Button>)}
                    </Td>
                  </Tr>
                )
              })}
            </Tbody>
          </Table>
        )}
        {d?.payments?.length > 0 && <p className="text-xs text-tertiary">Возврат звёзд не сокращает подписку — при необходимости сократите её в карточке пользователя.</p>}
      </CardBody>
    </Card>
  )
}

export default function TelegramBotPage() {
  const { can } = useAuth()
  const toast = useToast()
  const canWrite = can('settings.write')
  const [s, setS] = useState(null)
  const [loadErr, setLoadErr] = useState('')
  const [token, setToken] = useState('')
  const [channel, setChannel] = useState('')
  const [saving, setSaving] = useState('')
  const [botCheck, setBotCheck] = useState(null)
  const [chCheck, setChCheck] = useState(null)
  const [checking, setChecking] = useState('')
  const [confirmClear, setConfirmClear] = useState(false)

  async function load() {
    try {
      const d = await fetchTelegramSettings()
      setS(d)
      setChannel(d.channel?.id || '')
      setLoadErr('')
    } catch (e) { setLoadErr(e.message) }
  }
  useEffect(() => { load() }, [])

  async function save(payload, what) {
    setSaving(what)
    try {
      const d = await saveTelegramSettings(payload)
      setS(d)
      setToken('')
      setConfirmClear(false)
      if (d.setup_errors?.length) toast.warn(`Сохранено, но не всё настроилось: ${d.setup_errors.join('; ')}`)
      else toast.success(what === 'token' ? `Бот @${d.bot?.username} подключён: меню и кнопка «Кабинет» поставлены` : 'Сохранено')
    } catch (e) { toast.error(e.message) } finally { setSaving('') }
  }

  async function checkBot() {
    setChecking('bot')
    try {
      const r = await checkTelegramBot()
      const route = r.direct_down ? 'через exit' : 'напрямую'
      setBotCheck({ ok: true, text: `@${r.username} отвечает (${route}). ${r.setup_errors?.length ? 'Не настроилось: ' + r.setup_errors.join('; ') : 'Меню и кнопка «Кабинет» на месте.'}` })
    } catch (e) { setBotCheck({ ok: false, text: e.message }) } finally { setChecking('') }
  }

  async function checkChannel() {
    setChecking('channel')
    try {
      const r = await checkTelegramChannel()
      setChCheck(r.ok
        ? { ok: true, text: `«${r.title}»: бот — ${r.status === 'creator' ? 'владелец' : 'администратор'}, публикует${r.can_edit ? ' и редактирует' : ', но не может редактировать — правка новостей в канале не сработает'}.` }
        : { ok: false, text: r.error || 'Бот не может публиковать в этом канале' })
    } catch (e) { setChCheck({ ok: false, text: e.message }) } finally { setChecking('') }
  }

  if (loadErr) {
    return (
      <div className="max-w-3xl space-y-4">
        <PageHeader title="Telegram-бот" />
        <div role="alert" className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
          <span className="text-sm text-danger">Не удалось загрузить настройки: {loadErr}</span>
          <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Повторить</Button>
        </div>
      </div>
    )
  }
  if (!s) return <div className="flex justify-center py-20"><Spinner size="md" /></div>
  const bot = s.bot || {}

  return (
    <div className="max-w-5xl space-y-6">
      <PageHeader title="Telegram-бот" subtitle="Бот для пользователей: кабинет (Mini App), уведомления, поддержка и новости. Все вызовы Telegram идут с Hub через exit." />

      <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,360px)] gap-6 items-start">
        <div className="space-y-6 min-w-0">
          <Card>
            <CardHeader>
              <div className="flex items-center gap-2">
                <h3 className="text-sm font-semibold text-primary">Бот для пользователей</h3>
                {bot.token_set ? <Badge variant="success">подключён</Badge> : <Badge variant="warning">нет токена</Badge>}
              </div>
            </CardHeader>
            <CardBody className="space-y-4">
              {bot.token_set && (
                <dl className="grid grid-cols-[120px_minmax(0,1fr)] gap-y-2 text-sm">
                  <dt className="text-tertiary">Бот</dt>
                  <dd>{bot.username ? <a href={bot.bot_url} target="_blank" rel="noopener" className="font-mono text-accent hover:underline">@{bot.username}</a> : <span className="text-tertiary">—</span>}</dd>
                  <dt className="text-tertiary">Mini App</dt>
                  <dd className="flex items-center gap-2 min-w-0"><span className="font-mono truncate">{bot.mini_app_url}</span><CopyButton text={bot.mini_app_url} /></dd>
                  <dt className="text-tertiary">Путь</dt>
                  <dd className="text-secondary">{s.route?.direct_down ? 'через exit (напрямую Telegram сейчас недоступен)' : 'напрямую, при сбое — через exit'}</dd>
                </dl>
              )}
              <div>
                <label htmlFor="tg-bot-token" className="text-xs text-tertiary block mb-1">Токен бота</label>
                <Input id="tg-bot-token" type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)} className="font-mono"
                  placeholder={bot.token_set ? `Сохранён: ${bot.token_hint} — введите новый, чтобы заменить` : '123456789:AA…'} disabled={!canWrite} />
                <p className="text-xs text-tertiary mt-1">Хранится на сервере и в админку не возвращается. Нужен отдельный бот — не бот алертов.</p>
              </div>
              {canWrite && (
                <div className="flex flex-wrap gap-2">
                  <Button onClick={() => save({ bot_token: token }, 'token')} loading={saving === 'token'} disabled={!token.trim()}>Сохранить</Button>
                  <Button variant="secondary" icon={<Send className="w-3.5 h-3.5" />} onClick={checkBot} loading={checking === 'bot'} disabled={!bot.token_set}>Проверить</Button>
                  {bot.token_set && !confirmClear && <Button variant="ghost" onClick={() => setConfirmClear(true)}>Удалить токен</Button>}
                </div>
              )}
              {confirmClear && (
                <div role="alert" className="flex flex-wrap items-center gap-2 rounded border border-danger/30 bg-danger/10 px-3 py-2">
                  <span className="basis-full text-xs text-primary">Удалить токен? Бот перестанет отвечать, уведомления и вход в Mini App остановятся.</span>
                  <Button variant="danger" size="sm" onClick={() => save({ clear_token: true }, 'clear')} loading={saving === 'clear'}>Да, удалить</Button>
                  <Button variant="ghost" size="sm" onClick={() => setConfirmClear(false)}>Отмена</Button>
                </div>
              )}
              <Result r={botCheck} />
            </CardBody>
          </Card>

          <Card>
            <CardHeader><h3 className="text-sm font-semibold text-primary">Канал для новостей</h3></CardHeader>
            <CardBody className="space-y-4">
              <div>
                <label htmlFor="tg-channel" className="text-xs text-tertiary block mb-1">Канал</label>
                <Input id="tg-channel" value={channel} onChange={e => setChannel(e.target.value)} placeholder="@hiearth_news или -1001234567890" className="font-mono" disabled={!canWrite} />
                <p className="text-xs text-tertiary mt-1">Бот должен быть администратором канала с правом публикации. Канал с @именем покажется в кабинете кнопкой «Канал в Telegram».</p>
              </div>
              {canWrite && (
                <div className="flex flex-wrap gap-2">
                  <Button onClick={() => save({ channel_id: channel.trim() }, 'channel')} loading={saving === 'channel'} disabled={channel.trim() === (s.channel?.id || '')}>Сохранить</Button>
                  <Button variant="secondary" onClick={checkChannel} loading={checking === 'channel'} disabled={!s.channel?.id || !bot.token_set}>Проверить канал</Button>
                </div>
              )}
              <Result r={chCheck} />
            </CardBody>
          </Card>

          <StarsCard canWrite={canWrite} canRefund={can('payments.refund')} />

          <Card>
            <CardHeader><h3 className="text-sm font-semibold text-primary">Бот алертов</h3></CardHeader>
            <CardBody className="space-y-2 text-sm">
              <p className="text-secondary">
                {s.alerts?.token_set && s.alerts?.chat_id
                  ? <>Настроен, чат <span className="font-mono text-primary">{s.alerts.chat_id}</span>.</>
                  : <span className="text-warning">Не настроен — сбои и новые обращения в поддержку никуда не приходят.</span>}
              </p>
              <p className="text-tertiary">Туда же приходят новые обращения в поддержку. Ответ на такое сообщение (reply) уходит человеку в бот, на сайт и на почту.</p>
              <Link to="/alerts" className="text-accent hover:underline">Настройки алертов →</Link>
            </CardBody>
          </Card>
        </div>

        <Card>
          <CardHeader><h3 className="text-sm font-semibold text-primary">Как настроить</h3></CardHeader>
          <CardBody>
            <ol className="space-y-3">
              {STEPS.map(([t, d], i) => (
                <li key={t} className="flex gap-3">
                  <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded bg-accent/10 font-mono text-xs text-accent">{i + 1}</span>
                  <div className="min-w-0">
                    <p className="text-sm font-medium text-primary">{t}</p>
                    <p className="text-xs text-secondary mt-0.5">{d}</p>
                  </div>
                </li>
              ))}
            </ol>
          </CardBody>
        </Card>
      </div>
    </div>
  )
}
