import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiFetch } from '../api/http.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { addToHomeScreen, homeScreenStatus, isMiniApp } from '../lib/telegram.js'
import Alert from './Alert.jsx'
import Badge from './Badge.jsx'
import Button from './Button.jsx'
import Dialog, { DialogActions } from './Dialog.jsx'
import Skeleton from './Skeleton.jsx'
import { Switch } from './Controls.jsx'
import { useToast } from './Toast.jsx'

// «Telegram» in the settings (spec 08 §8.4): connect the HiEarth bot with a 10-minute
// t.me/<bot>?start=link_… link, unlink, switch the bot's reminders and news.
const linkBtn = { background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }

export function useTelegramLink(token) {
  const [data, setData] = useState(null)
  const [error, setError] = useState(false)
  const load = () => apiFetch('/api/v1/user/telegram', { token })
    .then((d) => { setData(d); setError(false); return d })
    .catch((e) => { if (e.kind !== 'auth') setError(true); return null })
  useEffect(() => { load() }, [token]) // eslint-disable-line react-hooks/exhaustive-deps
  return { data, setData, error, load }
}

export default function TelegramLinkCard({ state }) {
  const { token, logout } = useAuth()
  const toast = useToast()
  const navigate = useNavigate()
  const { data, setData, error, load } = state
  const [busy, setBusy] = useState(false)
  const [waitUrl, setWaitUrl] = useState('')
  const [unlink, setUnlink] = useState(false)
  const [unlinkBusy, setUnlinkBusy] = useState(false)
  const [switching, setSwitching] = useState('')
  const [home, setHome] = useState('')
  const until = useRef(0)
  const mini = isMiniApp()

  useEffect(() => { if (mini) homeScreenStatus().then(setHome) }, [mini])

  // after «Подключить»: ask every 3 s for 2 minutes (while the tab is visible) whether the bot linked it
  useEffect(() => {
    if (!waitUrl) return undefined
    const t = setInterval(async () => {
      if (Date.now() > until.current) { clearInterval(t); return }
      if (document.visibilityState !== 'visible') return
      const d = await load()
      if (d?.linked) {
        clearInterval(t)
        setWaitUrl('')
        toast.ok('Telegram подключён')
      }
    }, 3000)
    return () => clearInterval(t)
  }, [waitUrl]) // eslint-disable-line react-hooks/exhaustive-deps

  async function connect() {
    if (busy) return
    // the tab opens in the click itself (a popup blocker lets it through), the address follows
    const win = window.open('about:blank', '_blank')
    if (win) win.opener = null
    setBusy(true)
    try {
      const r = await apiFetch('/api/v1/user/telegram/link', { token, method: 'POST' })
      if (win) win.location.href = r.url
      else window.location.href = r.url
      until.current = Date.now() + 2 * 60 * 1000
      setWaitUrl(r.url)
    } catch (e) {
      win?.close()
      toast.bad(e.message)
    } finally {
      setBusy(false)
    }
  }

  async function doUnlink() {
    setUnlinkBusy(true)
    try {
      await apiFetch('/api/v1/user/telegram', { token, method: 'DELETE' })
      setUnlink(false)
      toast.ok('Telegram отвязан')
      if (mini) { logout(); navigate('/tg', { replace: true }); return }
      setData((d) => ({ ...d, linked: false, username: '' }))
    } catch (e) {
      toast.bad('Не удалось отвязать — попробуйте ещё раз')
    } finally {
      setUnlinkBusy(false)
    }
  }

  async function toggle(key, on) {
    const field = key === 'news' ? 'notify_news' : 'notify_reminders'
    const prev = data[field]
    setData((d) => ({ ...d, [field]: on }))
    setSwitching(key)
    try {
      const r = await apiFetch('/api/v1/user/telegram/notify', { token, method: 'PUT', body: { [key]: on } })
      setData((d) => ({ ...d, ...r }))
    } catch {
      setData((d) => ({ ...d, [field]: prev }))
      toast.bad('Не удалось изменить уведомления')
    } finally {
      setSwitching('')
    }
  }

  if (error && !data) {
    return <div className="card"><Alert tone="bad" title="Не удалось загрузить данные.">Обновите страницу. <button type="button" className="link" onClick={load} style={linkBtn}>Повторить</button></Alert></div>
  }
  if (!data) return <div className="card stack gap-3" aria-busy="true"><Skeleton w="50%" h={18} /><Skeleton w="70%" h={14} /></div>

  if (!data.linked) {
    return (
      <div className="card stack gap-3">
        <div className="row wrap gap-3" style={{ alignItems: 'center' }}>
          <Button variant="secondary" icon="send" loading={busy} onClick={connect}>Подключить Telegram</Button>
        </div>
        <p className="t-sm subtle">Откроется бот HiEarth — нажмите в нём «Запустить». Ссылка действует 10 минут.</p>
        {waitUrl && (
          <Alert tone="info" title="Ждём подтверждения в Telegram." rise>
            Нажмите «Запустить» в боте. <a href={waitUrl} target="_blank" rel="noopener" className="link">Открыть ссылку ещё раз</a>
          </Alert>
        )}
      </div>
    )
  }

  return (
    <div className="card stack gap-3">
      <div className="row between wrap gap-2">
        <span className="row wrap gap-3">
          <b className="mono">{data.username ? `@${data.username}` : 'Telegram'}</b>
          {data.bot_blocked ? <Badge tone="warn">Бот заблокирован</Badge> : <Badge tone="ok">Подключён</Badge>}
        </span>
        <span className="row gap-2">
          {data.bot_url && <Button variant="ghost" size="sm" href={data.bot_url} target="_blank" rel="noopener">Открыть бота</Button>}
          <Button variant="ghost" size="sm" onClick={() => setUnlink(true)}>Отвязать</Button>
        </span>
      </div>
      {data.bot_blocked && <p className="t-sm warn">Разблокируйте бота в Telegram, чтобы получать сообщения.</p>}
      <div className="row between gap-3" style={{ paddingTop: 12, borderTop: '1px solid var(--line)' }}>
        <label htmlFor="tg-rem" style={{ cursor: 'pointer' }}><b>Напоминания о подписке</b><br /><span className="t-sm subtle">За 3 дня и в последний день</span></label>
        <Switch id="tg-rem" checked={!!data.notify_reminders} disabled={switching === 'reminders'} onChange={(v) => toggle('reminders', v)} />
      </div>
      <div className="row between gap-3">
        <label htmlFor="tg-news" style={{ cursor: 'pointer' }}><b>Новости и советы</b><br /><span className="t-sm subtle">Не чаще раза в неделю</span></label>
        <Switch id="tg-news" checked={!!data.notify_news} disabled={switching === 'news'} onChange={(v) => toggle('news', v)} />
      </div>
      {mini && (home === 'missed' || home === 'unknown') && (
        <div className="row between gap-3" style={{ paddingTop: 12, borderTop: '1px solid var(--line)' }}>
          <span><b>Иконка на экране «Домой»</b><br /><span className="t-sm subtle">Кабинет откроется в один тап</span></span>
          <Button variant="ghost" size="sm" onClick={() => { addToHomeScreen(); setHome('added') }}>Добавить</Button>
        </div>
      )}

      <Dialog open={unlink} onClose={() => setUnlink(false)} title="Отвязать Telegram?" busy={unlinkBusy}>
        <p className="muted t-sm" style={{ marginTop: 8 }}>Напоминания, ответы поддержки и новости перестанут приходить в Telegram. Аккаунт и подписка останутся.</p>
        <DialogActions>
          <Button variant="ghost" onClick={() => setUnlink(false)} disabled={unlinkBusy} data-autofocus>Отмена</Button>
          <Button variant="danger" onClick={doUnlink} loading={unlinkBusy}>Отвязать</Button>
        </DialogActions>
      </Dialog>
    </div>
  )
}
