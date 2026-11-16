import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { confirmEmail, unsubscribeEmail } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import MiniPage from '../components/MiniPage.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import Button from '../components/Button.jsx'
import usePageMeta from '../hooks/usePageMeta.js'

// The two links from the letters (spec 06 §6.6, artboard «Письма»): /email/confirm?t=… confirms the
// address as soon as it opens; /email/unsubscribe?u=…&s=… switches the reminders off only after the
// button is pressed (so a mail scanner opening the link changes nothing).
function useGo() {
  const { isLoggedIn } = useAuth()
  return (to) => (isLoggedIn ? to : `/login?redirect=${encodeURIComponent(to)}`)
}

// a link that the server rejects (400/404) is «устарела»; anything else is a temporary failure
function Failed({ temporary, onRetry, go }) {
  return (
    <div className="swap stack" style={{ alignItems: 'center', gap: 12 }}>
      <Hedgehog mood="worried" size={120} />
      <h1 className="t-h3">Не получилось</h1>
      <p className="muted t-sm">
        {temporary
          ? 'Сервис временно недоступен. Попробуйте ещё раз через минуту.'
          : 'Ссылка устарела или уже использована. Запросите новое письмо в настройках кабинета.'}
      </p>
      {temporary
        ? <Button variant="secondary" size="sm" onClick={onRetry}>Повторить</Button>
        : <Button variant="secondary" size="sm" to={go('/settings')}>Настройки</Button>}
    </div>
  )
}

export function EmailConfirmPage() {
  usePageMeta('Подтверждение почты — HiEarth', { noindex: true })
  const [params] = useSearchParams()
  const go = useGo()
  const [state, setState] = useState('wait') // wait | ok | bad | temp
  const once = useRef(false)
  const run = () => {
    setState('wait')
    confirmEmail(params.get('t') || '')
      .then(() => setState('ok'))
      .catch((e) => setState(e.status === 400 || e.status === 404 ? 'bad' : 'temp'))
  }
  useEffect(() => {
    if (once.current) return
    once.current = true
    run()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <MiniPage label="Подтверждение почты">
      {state === 'wait' && (
        <div key="wait" className="swap stack" style={{ alignItems: 'center', gap: 12 }}>
          <span className="spinner" aria-hidden="true" style={{ margin: '24px 0 8px' }} />
          <h1 className="t-h3">Подтверждаем…</h1>
          <p className="muted t-sm" role="status">Пара секунд.</p>
        </div>
      )}
      {state === 'ok' && (
        <div key="ok" className="swap stack" style={{ alignItems: 'center', gap: 12 }}>
          <Hedgehog mood="happy" size={120} />
          <h1 className="t-h3">Почта подтверждена</h1>
          <p className="muted t-sm">Пришлём чек об оплате и напомним за 3 дня до конца подписки. Больше ничего.</p>
          <Button variant="primary" to={go('/dashboard')}>В кабинет</Button>
        </div>
      )}
      {(state === 'bad' || state === 'temp') && <Failed key={state} temporary={state === 'temp'} onRetry={run} go={go} />}
    </MiniPage>
  )
}

export function EmailUnsubscribePage() {
  usePageMeta('Напоминания — HiEarth', { noindex: true })
  const [params] = useSearchParams()
  const go = useGo()
  const [state, setState] = useState('ask') // ask | busy | ok | bad | temp
  async function unsubscribe() {
    setState('busy')
    try {
      await unsubscribeEmail(params.get('u') || '', params.get('s') || '')
      setState('ok')
    } catch (e) {
      setState(e.status === 400 || e.status === 404 ? 'bad' : 'temp')
    }
  }
  return (
    <MiniPage label="Напоминания о конце подписки">
      {(state === 'ask' || state === 'busy') && (
        <div key="ask" className="swap stack" style={{ alignItems: 'center', gap: 12 }}>
          <Hedgehog mood="idle" size={120} />
          <h1 className="t-h3">Отключить напоминания?</h1>
          <p className="muted t-sm">Письма о конце подписки перестанут приходить. Чеки об оплате продолжат приходить.</p>
          <div className="row wrap gap-2" style={{ justifyContent: 'center' }}>
            <Button variant="ghost" to="/" disabled={state === 'busy'}>Не надо</Button>
            <Button variant="secondary" onClick={unsubscribe} loading={state === 'busy'}>Отписаться</Button>
          </div>
        </div>
      )}
      {state === 'ok' && (
        <div key="ok" className="swap stack" style={{ alignItems: 'center', gap: 12 }}>
          <Hedgehog mood="sleep" size={120} />
          <h1 className="t-h3">Напоминания отключены</h1>
          <p className="muted t-sm">Включить их снова можно в настройках кабинета.</p>
          <Button variant="secondary" to={go('/settings')}>Настройки</Button>
        </div>
      )}
      {(state === 'bad' || state === 'temp') && <Failed key={state} temporary={state === 'temp'} onRetry={unsubscribe} go={go} />}
    </MiniPage>
  )
}

