import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { confirmEmail, unsubscribeEmail } from '../api/user.js'
import { useNoIndex } from '../components/RequireAuth.jsx'

// The two links from the letters (cleanup plan E8): /email/confirm?t=… confirms an address on
// open; /email/unsubscribe?u=…&s=… switches the reminders off with one click on the button (a
// link scanner opening the page changes nothing).
export function EmailConfirmPage() {
  useNoIndex()
  const [params] = useSearchParams()
  const [state, setState] = useState({ status: 'wait', text: '' })
  const once = useRef(false)
  useEffect(() => {
    if (once.current) return
    once.current = true
    confirmEmail(params.get('t') || '')
      .then((r) => setState({ status: 'ok', text: `Почта ${r.email} подтверждена. Теперь чеки и напоминания приходят на неё.` }))
      .catch((e) => setState({ status: 'bad', text: e.message }))
  }, [params])
  return <Result title={state.status === 'ok' ? 'Почта подтверждена' : state.status === 'bad' ? 'Не получилось' : 'Подтверждаем…'} {...state} />
}

export function EmailUnsubscribePage() {
  useNoIndex()
  const [params] = useSearchParams()
  const [state, setState] = useState({ status: 'ask', text: 'Отключить письма-напоминания о конце подписки? Чеки об оплате продолжат приходить.' })
  async function go() {
    setState({ status: 'wait', text: '' })
    try {
      const r = await unsubscribeEmail(params.get('u') || '', params.get('s') || '')
      setState({ status: 'ok', text: (r.message || 'Напоминания отключены') + '. Включить их снова можно в настройках кабинета.' })
    } catch (e) {
      setState({ status: 'bad', text: e.message })
    }
  }
  return (
    <Result title={state.status === 'ok' ? 'Готово' : state.status === 'bad' ? 'Не получилось' : 'Напоминания'} {...state}>
      {state.status === 'ask' && <button onClick={go} className="btn-primary !py-2.5 !text-sm">Отписаться</button>}
    </Result>
  )
}

function Result({ title, status, text, children }) {
  return (
    <section className="min-h-[60vh] flex items-center justify-center px-4 py-12">
      <div className="card rounded-[22px] p-8 max-w-md w-full text-center">
        <h1 className="serif text-2xl font-semibold mb-3">{title}</h1>
        {status === 'wait' ? <div className="skeleton h-4 w-48 mx-auto rounded" /> : <p className={`text-sm mb-6 ${status === 'bad' ? 'text-danger' : 'text-text-secondary'}`}>{text}</p>}
        <div className="flex flex-wrap justify-center gap-2.5">
          {children}
          <Link to="/settings" className="btn-ghost !py-2.5 !text-sm">Настройки</Link>
        </div>
      </div>
    </section>
  )
}
