import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowLeft, RefreshCw, Search, Send } from 'lucide-react'
import { useToast } from '../components/Toast.jsx'
import { useUrlState } from '../hooks/useUrlState.js'
import { authImage, closeSupportTicket, fetchSupportTicket, fetchSupportTickets, markSupportRead, reopenSupportTicket, sendSupportAnswer } from '../api/admin.js'
import { formatDate } from '../utils/format.js'
import { Badge, Button, Card, EmptyState, Input, PageHeader, Spinner, Textarea } from '../components/ui/index.js'

// «Поддержка» (plan TG5): tickets from the site and the Telegram bot; the answer goes to the bot,
// the site and the confirmed e-mail. The owner can also answer by replying in the alerts bot.
const FILTERS = [
  { key: 'needs_answer', label: 'Ждут ответа', count: c => c.open },
  { key: 'answered', label: 'Отвечено', count: c => c.answered },
  { key: 'closed', label: 'Закрыто', count: c => c.closed },
  { key: 'all', label: 'Все' },
]
const STATUS = {
  open: { label: 'новое', variant: 'warning' },
  waiting: { label: 'ждёт ответа', variant: 'warning' },
  answered: { label: 'отвечено', variant: 'success' },
  closed: { label: 'закрыто', variant: 'default' },
}
const VIA = { site: 'сайт', bot: 'бот', admin: 'админка', owner_tg: 'Telegram владельца' }
const shortId = id => (id && id.length > 12 ? `${id.slice(0, 4)}…${id.slice(-4)}` : id || '—')

function AdminImage({ name }) {
  const [url, setUrl] = useState('')
  useEffect(() => {
    let alive = true
    let made = ''
    authImage(`/support/images/${name}`).then(u => { if (alive) { made = u; setUrl(u) } else URL.revokeObjectURL(u) }).catch(() => {})
    return () => { alive = false; if (made) URL.revokeObjectURL(made) }
  }, [name])
  if (!url) return <div className="mt-2 w-40 h-28 rounded border border-default bg-canvas" aria-hidden="true" />
  return (
    <a href={url} target="_blank" rel="noopener" className="block mt-2">
      <img src={url} alt="Картинка из обращения" className="max-w-[240px] max-h-[240px] rounded border border-default object-cover" />
    </a>
  )
}

function Conversation({ id, onChanged, onBack }) {
  const toast = useToast()
  const [d, setD] = useState(null)
  const [error, setError] = useState('')
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const [toggling, setToggling] = useState(false)
  const endRef = useRef(null)

  const current = useRef(id)
  current.current = id
  const load = useCallback(async () => {
    try {
      const data = await fetchSupportTicket(id)
      if (current.current !== id) return // switched to another ticket meanwhile
      setD(data)
      setError('')
      if (data.messages.some(m => m.author === 'user' && !m.read_at)) {
        markSupportRead(id).then(onChanged).catch(() => {})
      }
    } catch (e) { setError(e.message) }
  }, [id]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { setD(null); load() }, [load])
  useEffect(() => { endRef.current?.scrollIntoView({ block: 'end' }) }, [d?.messages?.length])

  async function send() {
    if (!text.trim() || sending) return
    setSending(true)
    try {
      await sendSupportAnswer(id, text.trim())
      setText('')
      toast.success('Ответ отправлен')
      await load()
      onChanged()
    } catch (e) { toast.error(e.message) } finally { setSending(false) }
  }

  async function toggle() {
    setToggling(true)
    try {
      if (d.ticket.status === 'closed') await reopenSupportTicket(id)
      else await closeSupportTicket(id)
      await load()
      onChanged()
    } catch (e) { toast.error(e.message) } finally { setToggling(false) }
  }

  if (error) {
    return (
      <div role="alert" className="flex items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
        <span className="text-sm text-danger">{error}</span>
        <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Повторить</Button>
      </div>
    )
  }
  if (!d) return <div className="flex justify-center py-16"><Spinner size="md" /></div>
  const u = d.user || {}
  const st = STATUS[d.ticket.status] || STATUS.open
  return (
    <div className="flex flex-col min-h-0 h-full">
      <div className="flex flex-wrap items-center gap-2 border-b border-default px-4 py-3">
        <Button variant="ghost" size="sm" className="lg:hidden" icon={<ArrowLeft className="w-3.5 h-3.5" />} onClick={onBack} aria-label="К списку" />
        <span className="text-sm font-semibold text-primary">№<span className="font-mono tabular-nums">{d.ticket.id}</span></span>
        <Badge variant={st.variant}>{st.label}</Badge>
        <Link to={`/users?search=${encodeURIComponent(u.user_id || '')}&open=${u.id}`} className="font-mono text-xs text-accent hover:underline">{shortId(u.user_id)}</Link>
        {u.username && <span className="font-mono text-xs text-secondary">{u.username}</span>}
        {u.telegram_username && <span className="font-mono text-xs text-secondary">@{u.telegram_username}</span>}
        <span className="text-xs text-tertiary">
          {u.telegram_linked ? 'ответ уйдёт в бот' : 'бот не привязан'}{u.email ? ' · и на почту' : ''}
        </span>
        <span className="ml-auto" />
        <Button variant="secondary" size="sm" onClick={toggle} loading={toggling}>{d.ticket.status === 'closed' ? 'Открыть снова' : 'Закрыть'}</Button>
      </div>
      <div className="flex-1 min-h-0 overflow-y-auto px-4 py-4 space-y-3" role="log" aria-live="polite">
        {d.messages.map(m => (
          <div key={m.id} className={`flex flex-col max-w-[85%] ${m.author === 'admin' ? 'ml-auto items-end' : 'items-start'}`}>
            <div className={`rounded-md border px-3 py-2 text-sm whitespace-pre-wrap break-words ${m.author === 'admin' ? 'border-accent/30 bg-accent/10 text-primary' : 'border-default bg-elevated text-primary'}`}>
              {m.body}
              {m.image && <AdminImage name={m.image} />}
            </div>
            <span className="mt-1 text-xs text-tertiary">
              <span className="font-mono tabular-nums">{formatDate(m.created_at)}</span> · {VIA[m.via] || m.via}
            </span>
          </div>
        ))}
        <div ref={endRef} />
      </div>
      <div className="border-t border-default px-4 py-3 space-y-2">
        <label htmlFor="support-answer" className="sr-only">Ответ</label>
        <Textarea id="support-answer" rows={3} value={text} maxLength={4000} placeholder="Ответ — придёт в бот, на сайт и на почту"
          onChange={e => setText(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send() }} />
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs text-tertiary">Ctrl+Enter — отправить{text.length > 3500 ? ` · ${text.length} из 4000` : ''}</span>
          <Button icon={<Send className="w-3.5 h-3.5" />} onClick={send} loading={sending} disabled={!text.trim()}>Ответить</Button>
        </div>
      </div>
    </div>
  )
}

export default function SupportPage() {
  const [f, setF] = useUrlState({ status: 'needs_answer', q: '', ticket: 0 })
  const [items, setItems] = useState([])
  const [counts, setCounts] = useState({ open: 0, answered: 0, closed: 0, unread: 0 })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [q, setQ] = useState(f.q)

  const load = useCallback(async () => {
    try {
      const d = await fetchSupportTickets({ status: f.status, q: f.q })
      setItems(d.items || [])
      setCounts(d.counts || {})
      setError('')
    } catch (e) { setError(e.message) } finally { setLoading(false) }
  }, [f.status, f.q])

  useEffect(() => { load() }, [load])
  useEffect(() => {
    const t = setInterval(() => { if (document.visibilityState === 'visible') load() }, 30000)
    return () => clearInterval(t)
  }, [load])
  useEffect(() => { const t = setTimeout(() => { if (q !== f.q) setF({ q }) }, 300); return () => clearTimeout(t) }, [q]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="max-w-7xl space-y-4">
      <PageHeader title="Поддержка" subtitle="Обращения с сайта и из Telegram-бота. Ответ уходит в бот, на сайт и на подтверждённую почту. Ответить можно и прямо в боте алертов — ответом на уведомление." />
      <div className="flex flex-col sm:flex-row sm:items-center gap-2">
        <div role="tablist" aria-label="Фильтр обращений" className="flex flex-wrap gap-1">
          {FILTERS.map(x => (
            <button key={x.key} role="tab" aria-selected={f.status === x.key} onClick={() => setF({ status: x.key, ticket: 0 })}
              className={`h-8 px-3 rounded text-sm border ${f.status === x.key ? 'border-accent/40 bg-accent/10 text-primary' : 'border-default text-secondary hover:text-primary hover:bg-elevated'}`}>
              {x.label}{x.count && <span className="ml-1.5 font-mono tabular-nums text-tertiary">{x.count(counts) || 0}</span>}
            </button>
          ))}
        </div>
        <div className="relative sm:ml-auto w-full sm:w-72">
          <Search className="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-tertiary" aria-hidden="true" />
          <Input value={q} onChange={e => setQ(e.target.value)} placeholder="HiEarth ID, логин или @telegram" className="pl-8" aria-label="Поиск обращений" />
        </div>
      </div>

      {error && <div role="alert" className="rounded border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">{error}</div>}

      <Card className="overflow-hidden">
        <div className="grid lg:grid-cols-[minmax(0,360px)_minmax(0,1fr)] h-[calc(100vh-240px)] min-h-[480px]">
          <div className={`border-r border-default overflow-y-auto ${f.ticket ? 'hidden lg:block' : ''}`}>
            {loading ? <div className="flex justify-center py-16"><Spinner size="md" /></div>
              : items.length === 0 ? <EmptyState title="Обращений нет" description={f.status === 'needs_answer' ? 'Все вопросы отвечены.' : 'Здесь появятся обращения с сайта и из бота.'} />
                : items.map(t => {
                  const st = STATUS[t.status] || STATUS.open
                  return (
                    <button key={t.id} onClick={() => setF({ ticket: t.id })} aria-current={f.ticket === t.id ? 'true' : undefined}
                      className={`w-full text-left px-4 py-3 border-b border-default hover:bg-elevated ${f.ticket === t.id ? 'bg-accent/10 border-l-2 border-l-accent' : ''}`}>
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-xs text-primary">{shortId(t.user_id)}</span>
                        {t.telegram_username && <span className="font-mono text-xs text-tertiary truncate">@{t.telegram_username}</span>}
                        <span className="ml-auto font-mono tabular-nums text-xs text-tertiary">{formatDate(t.updated_at)}</span>
                      </div>
                      <div className="mt-1 flex items-start gap-2">
                        <p className={`text-sm line-clamp-2 flex-1 ${t.unread ? 'text-primary font-medium' : 'text-secondary'}`}>
                          {t.last_author === 'admin' && <span className="text-tertiary">Вы: </span>}{t.last_body}
                        </p>
                        {t.unread > 0 && <span className="shrink-0 rounded bg-accent/15 px-1.5 font-mono text-xs tabular-nums text-accent">{t.unread}</span>}
                      </div>
                      <div className="mt-1"><Badge variant={st.variant}>{st.label}</Badge></div>
                    </button>
                  )
                })}
          </div>
          <div className={`min-h-0 ${f.ticket ? '' : 'hidden lg:flex lg:items-center lg:justify-center'}`}>
            {f.ticket
              ? <Conversation id={f.ticket} onChanged={load} onBack={() => setF({ ticket: 0 })} />
              : <p className="text-sm text-tertiary">Выберите обращение слева</p>}
          </div>
        </div>
      </Card>
    </div>
  )
}
