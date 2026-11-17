import { Fragment, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ImagePlus, Plus, RefreshCw, Send, Trash2 } from 'lucide-react'
import { useToast } from '../components/Toast.jsx'
import { createNews, deleteNews, fetchNews, scheduleNews, sendNews, unscheduleNews, updateNews, uploadNewsImage } from '../api/admin.js'
import { formatDate } from '../utils/format.js'
import { Badge, Button, Card, Dialog, EmptyState, Input, PageHeader, TableSkeleton, Table, Thead, Tbody, Tr, Th, Td, Textarea } from '../components/ui/index.js'

// «Новости» (plan TG6): one post → the Telegram channel, the bot (all / with a subscription /
// without), the cabinet feed and Happ's announce line.
const TARGETS = [
  { key: 'channel', label: 'Канал' },
  { key: 'bot_all', label: 'Бот — всем', count: 'bot_all' },
  { key: 'bot_active', label: 'Бот — с подпиской', count: 'bot_active' },
  { key: 'bot_inactive', label: 'Бот — без подписки', count: 'bot_inactive' },
  { key: 'site', label: 'Лента в кабинете' },
  { key: 'happ', label: 'Объявление в Happ' },
]
const STATUS = {
  draft: { label: 'черновик', variant: 'default' },
  scheduled: { label: 'по расписанию', variant: 'info' },
  sending: { label: 'отправляется', variant: 'warning' },
  sent: { label: 'отправлено', variant: 'success' },
  failed: { label: 'ошибка', variant: 'danger' },
}
const EMPTY = { title: '', body: '', image: '', button_text: '', button_url: '', targets: ['channel', 'bot_all', 'site'], happ_days: 3 }

// Preview of the Telegram HTML subset (allow-list, the same as the cabinet's RichText).
const TAGS = { b: 'b', strong: 'b', i: 'i', em: 'i', u: 'u', ins: 'u', s: 's', strike: 's', del: 's', code: 'code', pre: 'pre', blockquote: 'blockquote', 'tg-spoiler': 'span', a: 'a' }
function toNodes(node, key) {
  if (node.nodeType === 3) return node.nodeValue
  if (node.nodeType !== 1) return null
  const name = node.nodeName.toLowerCase()
  const kids = Array.from(node.childNodes).map((c, i) => toNodes(c, i))
  const Tag = TAGS[name]
  if (!Tag) return <Fragment key={key}>{kids}</Fragment>
  if (name === 'a') {
    const href = node.getAttribute('href') || ''
    return /^https?:\/\//.test(href) ? <a key={key} href={href} target="_blank" rel="noopener noreferrer" className="text-accent underline">{kids}</a> : <Fragment key={key}>{kids}</Fragment>
  }
  if (name === 'blockquote') return <blockquote key={key} className="border-l-2 border-strong pl-2 text-secondary">{kids}</blockquote>
  return <Tag key={key} className={name === 'code' || name === 'pre' ? 'font-mono text-xs' : undefined}>{kids}</Tag>
}
function Rich({ html }) {
  const nodes = useMemo(() => {
    try { return Array.from(new DOMParser().parseFromString(`<body>${html || ''}</body>`, 'text/html').body.childNodes).map((n, i) => toNodes(n, i)) } catch { return html }
  }, [html])
  return <div className="whitespace-pre-wrap break-words">{nodes}</div>
}

function Preview({ form }) {
  const img = form.image ? `/api/v1/news/images/${form.image}` : ''
  return (
    <div className="space-y-4">
      <div>
        <p className="text-xs text-tertiary mb-1">Как в Telegram</p>
        <div className="rounded-md border border-default bg-canvas p-3 text-sm text-primary space-y-2">
          {img && <img src={img} alt="" className="w-full max-h-48 object-cover rounded" />}
          {form.title && <p className="font-semibold">{form.title}</p>}
          {form.body && <Rich html={form.body} />}
          {form.button_text && <div className="rounded border border-accent/40 bg-accent/10 py-1.5 text-center text-xs text-accent">{form.button_text}</div>}
        </div>
      </div>
      <div>
        <p className="text-xs text-tertiary mb-1">Как в кабинете</p>
        <div className="rounded-md border border-default bg-elevated p-3 text-sm text-primary space-y-2">
          {img && <img src={img} alt="" className="w-full max-h-48 object-cover rounded" />}
          <p className="text-xs text-tertiary">сегодня</p>
          {form.title && <p className="text-base font-semibold">{form.title}</p>}
          {form.body && <Rich html={form.body} />}
          {form.button_text && <span className="inline-block rounded border border-strong px-2 py-1 text-xs">{form.button_text}</span>}
        </div>
      </div>
      {form.targets.includes('happ') && (
        <div>
          <p className="text-xs text-tertiary mb-1">Строка в Happ</p>
          <p className="font-mono text-xs text-secondary">📣 {(form.title || form.body.replace(/<[^>]+>/g, '')).slice(0, 59)}</p>
        </div>
      )}
    </div>
  )
}

function Editor({ post, audience, channel, onClose, onSaved }) {
  const toast = useToast()
  const sent = post?.status === 'sent'
  const [form, setForm] = useState(() => (post ? { ...EMPTY, ...post, targets: post.targets || [] } : EMPTY))
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [confirm, setConfirm] = useState(false)
  const [at, setAt] = useState('')
  const set = (k, v) => { setForm(f => ({ ...f, [k]: v })); setErr('') }
  const toggle = k => set('targets', form.targets.includes(k) ? form.targets.filter(x => x !== k) : [...form.targets, k])
  const botCount = form.targets.includes('bot_all') || (form.targets.includes('bot_active') && form.targets.includes('bot_inactive'))
    ? audience.bot_all : (form.targets.includes('bot_active') ? audience.bot_active : 0) + (form.targets.includes('bot_inactive') ? audience.bot_inactive : 0)

  async function save() {
    setBusy('save')
    try {
      const payload = { title: form.title, body: form.body, image: form.image, button_text: form.button_text, button_url: form.button_url, targets: form.targets, happ_days: Number(form.happ_days) || 3 }
      const p = post ? await updateNews(post.id, payload) : await createNews(payload)
      toast.success(sent ? 'Сохранено — правка уходит в канал и бот' : 'Черновик сохранён')
      onSaved(p)
      return p
    } catch (e) { setErr(e.message); return null } finally { setBusy('') }
  }

  async function send() {
    const p = await save()
    if (!p) return
    setBusy('send')
    try {
      const r = await sendNews(p.id)
      if (r.error) toast.warn(`Отправлено с ошибкой: ${r.error}`)
      else toast.success('Новость отправлена')
      setConfirm(false)
      onSaved(r)
      onClose()
    } catch (e) { setErr(e.message); setConfirm(false) } finally { setBusy('') }
  }

  async function schedule() {
    if (!at) { setErr('Укажите дату и время'); return }
    const p = await save()
    if (!p) return
    setBusy('schedule')
    try {
      const r = await scheduleNews(p.id, new Date(at).toISOString())
      toast.success(`Запланировано на ${formatDate(r.scheduled_at)}`)
      onSaved(r)
      onClose()
    } catch (e) { setErr(e.message) } finally { setBusy('') }
  }

  async function upload(e) {
    const f = e.target.files?.[0]
    e.target.value = ''
    if (!f) return
    setBusy('upload')
    try {
      const r = await uploadNewsImage(f)
      set('image', r.image)
    } catch (e2) { setErr(e2.message) } finally { setBusy('') }
  }

  return (
    <Dialog open onClose={onClose} title={post ? (sent ? 'Правка отправленной новости' : 'Новость') : 'Новая новость'} maxWidth="max-w-4xl">
      <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,300px)] gap-5">
        <div className="space-y-3 min-w-0">
          {sent && <p className="rounded border border-info/30 bg-info/10 px-3 py-2 text-xs text-primary">Изменения уйдут в канал и в сообщения бота за последние 47 часов. Места публикации и картинку у отправленной новости не меняют.</p>}
          <div>
            <label htmlFor="news-title" className="text-xs text-tertiary block mb-1">Заголовок</label>
            <Input id="news-title" value={form.title} maxLength={200} onChange={e => set('title', e.target.value)} placeholder="Вход для мобильных сетей" />
          </div>
          <div>
            <label htmlFor="news-body" className="text-xs text-tertiary block mb-1">Текст</label>
            <Textarea id="news-body" rows={7} value={form.body} onChange={e => set('body', e.target.value)} className="font-mono text-xs" />
            <p className="text-xs text-tertiary mt-1">HTML Telegram: &lt;b&gt;, &lt;i&gt;, &lt;u&gt;, &lt;s&gt;, &lt;a href="https://…"&gt;, &lt;code&gt;, &lt;blockquote&gt;, &lt;tg-spoiler&gt;. Символы &lt; &gt; &amp; в тексте — как &amp;lt; &amp;gt; &amp;amp;. Без слова «VPN».</p>
          </div>
          <div className="flex flex-col sm:flex-row sm:items-center gap-2">
            {form.image
              ? <><span className="font-mono text-xs text-secondary truncate">{form.image}</span>{!sent && <Button variant="ghost" size="sm" onClick={() => set('image', '')}>Убрать картинку</Button>}</>
              : !sent && (
                <label className="inline-flex">
                  <input type="file" accept="image/jpeg,image/png,image/webp" className="sr-only" onChange={upload} />
                  <span className="inline-flex h-8 items-center gap-1.5 rounded border border-default px-3 text-sm text-secondary hover:bg-elevated cursor-pointer"><ImagePlus className="w-3.5 h-3.5" aria-hidden="true" />{busy === 'upload' ? 'Загружаем…' : 'Картинка'}</span>
                </label>
              )}
            <span className="text-xs text-tertiary">С картинкой текст — до 1024 символов</span>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <div>
              <label htmlFor="news-btn" className="text-xs text-tertiary block mb-1">Кнопка — текст</label>
              <Input id="news-btn" value={form.button_text} maxLength={40} onChange={e => set('button_text', e.target.value)} placeholder="Подробнее" />
            </div>
            <div>
              <label htmlFor="news-url" className="text-xs text-tertiary block mb-1">Кнопка — ссылка</label>
              <Input id="news-url" value={form.button_url} onChange={e => set('button_url', e.target.value)} placeholder="https://hiearth.ru/help" className="font-mono" />
            </div>
          </div>
          <fieldset disabled={sent} className="space-y-1.5">
            <legend className="text-xs text-tertiary mb-1">Куда</legend>
            {TARGETS.map(t => (
              <label key={t.key} className="flex items-center gap-2 text-sm text-primary">
                <input type="checkbox" checked={form.targets.includes(t.key)} onChange={() => toggle(t.key)} />
                {t.label}
                {t.key === 'channel' && <span className="font-mono text-xs text-tertiary">{channel || 'не настроен'}</span>}
                {t.count && <span className="font-mono tabular-nums text-xs text-tertiary">{audience[t.count] ?? 0}</span>}
                {t.key === 'happ' && form.targets.includes('happ') && (
                  <span className="flex items-center gap-1 text-xs text-tertiary">на <Input type="number" min={1} max={30} value={form.happ_days} onChange={e => set('happ_days', e.target.value)} className="w-16 h-7 font-mono" aria-label="Дней в Happ" /> дн.</span>
                )}
              </label>
            ))}
          </fieldset>
          {err && <div role="alert" className="rounded border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">{err}</div>}
          <div className="flex flex-col sm:flex-row sm:flex-wrap gap-2 pt-1">
            <Button variant="secondary" onClick={save} loading={busy === 'save'}>{sent ? 'Сохранить правку' : 'Сохранить черновик'}</Button>
            {!sent && <Button icon={<Send className="w-3.5 h-3.5" />} onClick={() => setConfirm(true)} disabled={!!busy}>Отправить сейчас…</Button>}
            {!sent && (
              <span className="flex items-center gap-2">
                <Input type="datetime-local" value={at} onChange={e => setAt(e.target.value)} className="w-auto font-mono" aria-label="Когда отправить" />
                <Button variant="ghost" onClick={schedule} loading={busy === 'schedule'}>Запланировать</Button>
              </span>
            )}
          </div>
          {confirm && (
            <div role="alert" className="space-y-2 rounded border border-warning/30 bg-warning/10 px-3 py-2">
              <p className="text-sm text-primary">Отправить «{form.title || 'без заголовка'}»?</p>
              <ul className="text-xs text-secondary list-disc pl-4">
                {form.targets.includes('channel') && <li>канал <span className="font-mono">{channel || '— не настроен'}</span></li>}
                {botCount > 0 && <li>бот: <span className="font-mono tabular-nums">{botCount}</span> получателей (≤ 25 в секунду)</li>}
                {form.targets.includes('site') && <li>лента в кабинете</li>}
                {form.targets.includes('happ') && <li>объявление в Happ на <span className="font-mono tabular-nums">{form.happ_days}</span> дн.</li>}
              </ul>
              <p className="text-xs text-tertiary">Отозвать нельзя — только исправить или удалить (бот удаляет свои сообщения за 48 часов).</p>
              <div className="flex gap-2">
                <Button size="sm" onClick={send} loading={busy === 'send'}>Да, отправить</Button>
                <Button variant="ghost" size="sm" onClick={() => setConfirm(false)} disabled={busy === 'send'}>Отмена</Button>
              </div>
            </div>
          )}
        </div>
        <Preview form={form} />
      </div>
    </Dialog>
  )
}

export default function NewsPage() {
  const toast = useToast()
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState(null) // null | 'new' | post
  const [removing, setRemoving] = useState(null)

  async function load() {
    try { setData(await fetchNews()); setError('') } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  async function remove(p) {
    try {
      await deleteNews(p.id)
      toast.success(p.status === 'sent' ? 'Удалено: сообщения в канале и боте удаляются' : 'Удалено')
      setRemoving(null)
      load()
    } catch (e) { toast.error(e.message) }
  }

  async function unschedule(p) {
    try { await unscheduleNews(p.id); load() } catch (e) { toast.error(e.message) }
  }

  const items = data?.items || []
  return (
    <div className="max-w-7xl space-y-4">
      <PageHeader title="Новости" subtitle="Одна публикация — в канал, в бот, в ленту кабинета и строкой в Happ."
        actions={<Button icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setEditing('new')}>Новая новость</Button>} />
      {data && !data.bot_ready && <p className="rounded border border-warning/30 bg-warning/10 px-3 py-2 text-sm text-primary">Бот ещё не подключён — канал и рассылка не сработают. <Link to="/telegram-bot" className="text-accent hover:underline">Настроить бота →</Link></p>}
      {error && (
        <div role="alert" className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
          <span className="text-sm text-danger">{error}</span>
          <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Повторить</Button>
        </div>
      )}
      {!data ? <TableSkeleton rows={4} cols={5} /> : items.length === 0 ? (
        <Card><EmptyState title="Новостей пока нет" description="Напишите первую — она уйдёт в канал, бот, кабинет и Happ." action={<Button onClick={() => setEditing('new')}>Новая новость</Button>} /></Card>
      ) : (
        <Table>
          <Thead><Tr><Th>Когда</Th><Th>Заголовок</Th><Th>Куда</Th><Th>Статус</Th><Th align="right">Бот: доставлено / ошибки / блок / в очереди</Th><Th align="right" /></Tr></Thead>
          <Tbody>
            {items.map(p => {
              const st = STATUS[p.status] || STATUS.draft
              return (
                <Tr key={p.id} onClick={() => setEditing(p)} className="cursor-pointer">
                  <Td mono>{formatDate(p.sent_at || p.scheduled_at || p.created_at)}</Td>
                  <Td><span className="text-primary">{p.title || <span className="text-tertiary">без заголовка</span>}</span>{p.error && <p className="text-xs text-danger truncate max-w-xs" title={p.error}>{p.error}</p>}</Td>
                  <Td><span className="text-xs text-secondary">{p.targets.map(t => TARGETS.find(x => x.key === t)?.label).filter(Boolean).join(', ')}</span></Td>
                  <Td><Badge variant={st.variant}>{st.label}</Badge></Td>
                  <Td mono align="right">{p.stats ? `${p.stats.sent} / ${p.stats.failed} / ${p.stats.blocked} / ${p.stats.queued}` : '—'}</Td>
                  <Td align="right">
                    <span className="inline-flex gap-1" onClick={e => e.stopPropagation()}>
                      {p.status === 'scheduled' && <Button variant="ghost" size="sm" onClick={() => unschedule(p)}>Снять</Button>}
                      <Button variant="ghost" size="sm" icon={<Trash2 className="w-3.5 h-3.5" />} aria-label="Удалить" onClick={() => setRemoving(p)} disabled={p.status === 'sending'} />
                    </span>
                  </Td>
                </Tr>
              )
            })}
          </Tbody>
        </Table>
      )}

      {editing && (
        <Editor post={editing === 'new' ? null : editing} audience={data?.audience || {}} channel={data?.channel || ''}
          onClose={() => setEditing(null)} onSaved={p => { setEditing(cur => (cur === 'new' || cur?.id === p.id ? p : cur)); load() }} />
      )}
      <Dialog open={!!removing} onClose={() => setRemoving(null)} title="Удалить новость?" maxWidth="max-w-md">
        <p className="text-sm text-secondary">{removing?.status === 'sent' ? 'Она исчезнет из ленты и Happ; бот удалит её в канале и у получателей (у кого прошло меньше 48 часов).' : 'Черновик удалится.'}</p>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="ghost" onClick={() => setRemoving(null)}>Отмена</Button>
          <Button variant="danger" onClick={() => remove(removing)}>Удалить</Button>
        </div>
      </Dialog>
    </div>
  )
}
