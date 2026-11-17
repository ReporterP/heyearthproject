import { useCallback, useEffect, useRef, useState } from 'react'
import { apiFetch } from '../api/http.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { isMiniApp } from '../lib/telegram.js'
import AppShell, { PageHead } from '../components/AppShell.jsx'
import Alert from '../components/Alert.jsx'
import Badge from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import EmptyState from '../components/EmptyState.jsx'
import Field, { Input } from '../components/Field.jsx'
import { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import Thread from '../components/Thread.jsx'
import { refreshSupportUnread } from '../components/SupportCount.js'
import usePageMeta from '../hooks/usePageMeta.js'

// Поддержка (spec 08 §8.6): the conversation, a new message with an optional picture.
const STATUS = {
  open: { tone: 'warn', text: 'Ждёт ответа' },
  waiting: { tone: 'warn', text: 'Ждёт ответа' },
  answered: { tone: 'ok', text: 'Есть ответ' },
  closed: { tone: undefined, text: 'Закрыто' },
}
const MAX = 4000
const MAX_IMG = 5 * 1024 * 1024

export default function SupportPage() {
  usePageMeta('Поддержка — HiEarth', { noindex: true })
  const { token } = useAuth()
  const [data, setData] = useState(null)
  const [loadErr, setLoadErr] = useState(false)
  const [unreadFrom, setUnreadFrom] = useState(null)
  const [text, setText] = useState('')
  const [file, setFile] = useState(null)
  const [fieldErr, setFieldErr] = useState('')
  const [formErr, setFormErr] = useState(null) // { tone, title, text }
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState(null)
  const fileRef = useRef(null)
  const endRef = useRef(null)
  const first = useRef(true)
  const showSkel = useDelayed(!data && !loadErr, 300)

  const load = useCallback(async (quiet = false) => {
    try {
      const d = await apiFetch('/api/v1/support', { token })
      setLoadErr(false)
      if (first.current) {
        first.current = false
        if (d.unread > 0) {
          const firstNew = d.messages.find((m) => m.author !== 'user' && !m.read_at)
          setUnreadFrom(firstNew?.id || null)
          apiFetch('/api/v1/support/read', { token, method: 'POST' }).then(refreshSupportUnread).catch(() => {})
        }
      } else if (d.unread > 0) {
        apiFetch('/api/v1/support/read', { token, method: 'POST' }).then(refreshSupportUnread).catch(() => {})
      }
      setData(d)
    } catch (e) {
      if (!quiet && e.kind !== 'auth') setLoadErr(true)
    }
  }, [token])

  useEffect(() => { load() }, [load])
  useEffect(() => {
    const t = setInterval(() => { if (document.visibilityState === 'visible') load(true) }, 30000)
    return () => clearInterval(t)
  }, [load])
  const count = (data?.messages?.length || 0) + (pending ? 1 : 0)
  useEffect(() => { if (count) endRef.current?.scrollIntoView({ block: 'end', behavior: 'smooth' }) }, [count])

  function pick(e) {
    const f = e.target.files?.[0]
    e.target.value = ''
    if (!f) return
    if (!/^image\/(jpeg|png|webp|gif)$/.test(f.type)) { setFieldErr('Можно приложить только картинку: JPG, PNG, WebP или GIF'); return }
    if (f.size > MAX_IMG) { setFieldErr('Картинка больше 5 МБ — уменьшите её или сделайте скриншот'); return }
    setFieldErr('')
    setFile(f)
  }

  async function send(e) {
    e?.preventDefault()
    if (busy) return
    const body = text.trim()
    if (!body && !file) { setFieldErr('Напишите сообщение или приложите картинку'); return }
    if (body.length > MAX) { setFieldErr('Сообщение длиннее 4000 символов — разделите его на несколько'); return }
    setBusy(true)
    setFieldErr('')
    setFormErr(null)
    const local = file ? URL.createObjectURL(file) : ''
    setPending({ id: 'pending', author: 'user', body, image: file ? 'local' : '', localUrl: local, sending: true })
    const form = new FormData()
    form.append('body', body)
    if (file) form.append('image', file)
    try {
      await apiFetch('/api/v1/support/messages', { token, method: 'POST', form, timeoutMs: 60000 })
      setText('')
      setFile(null)
      await load(true)
    } catch (err) {
      if (err.status === 400 || err.status === 413) setFieldErr(err.message)
      else if (err.status === 429) setFormErr({ tone: 'warn', title: 'Слишком много сообщений подряд.', text: 'Подождите несколько минут.' })
      else setFormErr({ tone: 'bad', title: 'Не удалось отправить.', text: 'Попробуйте через минуту.' })
    } finally {
      if (local) URL.revokeObjectURL(local)
      setPending(null)
      setBusy(false)
    }
  }

  const status = data?.ticket ? STATUS[data.ticket.status] : null
  const messages = data ? [...data.messages, ...(pending ? [pending] : [])] : []
  let body
  if (loadErr && !data) {
    body = (
      <EmptyState mood="worried" title="Не удалось загрузить переписку." actions={<Button variant="secondary" onClick={() => { setLoadErr(false); load() }}>Повторить</Button>}>
        Обновите страницу.
      </EmptyState>
    )
  } else if (!data) {
    body = <div aria-busy="true">{showSkel && <SkeletonCard lines={4} />}</div>
  } else {
    body = (
      <div className="stack gap-4">
        <section className="card" aria-label="Переписка">
          {messages.length
            ? <Thread messages={messages} unreadFrom={unreadFrom} image={(name) => `/api/v1/support/images/${name}`} />
            : <EmptyState small title="Напишите, что случилось">Опишите, что делали и что видите на экране. Можно приложить скриншот — так разберёмся быстрее.</EmptyState>}
          <div ref={endRef} />
        </section>
        {data.ticket?.status === 'closed' && <Alert tone="info" title="Обращение закрыто.">Напишите — откроем новое.</Alert>}
        <form className="card stack gap-3" onSubmit={send} noValidate>
          {formErr && <Alert tone={formErr.tone} title={formErr.title} rise>{formErr.text}</Alert>}
          <Field label="Сообщение" error={fieldErr || null} hint={text.length > 3500 ? `${text.length} из ${MAX}` : null}>
            <Input as="textarea" rows={4} value={text} invalid={!!fieldErr} maxLength={MAX + 200}
              onChange={(e) => { setText(e.target.value); setFieldErr('') }}
              onKeyDown={(e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send(e) }} />
          </Field>
          <div className="row wrap between gap-3">
            <div className="row wrap gap-2" style={{ alignItems: 'center' }}>
              <input ref={fileRef} type="file" accept="image/jpeg,image/png,image/webp,image/gif" hidden onChange={pick} />
              {file ? (
                <>
                  <span className="tag--chip mono" style={{ maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{file.name}</span>
                  <Button variant="ghost" size="sm" onClick={() => setFile(null)} disabled={busy}>Убрать</Button>
                </>
              ) : (
                <Button variant="ghost" size="sm" icon="plus" onClick={() => fileRef.current?.click()} disabled={busy}>Приложить картинку</Button>
              )}
              <span className="t-xs subtle">JPG, PNG или WebP до 5 МБ</span>
            </div>
            <Button variant="primary" type="submit" loading={busy}>{busy ? 'Отправляем…' : 'Отправить'}</Button>
          </div>
        </form>
        {!data.telegram_linked && data.bot_url && !isMiniApp() && (
          <section className="card card--elevated row wrap between gap-3" style={{ alignItems: 'center' }}>
            <span><b>Ответы удобно получать в Telegram</b><br /><span className="t-sm muted">Бот HiEarth пришлёт ответ, как только он появится.</span></span>
            <Button variant="secondary" size="sm" to="/settings#telegram">Подключить Telegram</Button>
          </section>
        )}
      </div>
    )
  }

  return (
    <AppShell>
      <div style={{ maxWidth: 820 }}>
        <PageHead title="Поддержка" lead="Ответим здесь и в Telegram — обычно в течение дня." actions={status && <Badge tone={status.tone}>{status.text}</Badge>} />
        {body}
      </div>
    </AppShell>
  )
}
