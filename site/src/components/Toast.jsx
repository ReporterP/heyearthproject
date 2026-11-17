import { createContext, useCallback, useContext, useMemo, useRef, useState } from 'react'
import Icon from './Icon.jsx'

// Toasts (spec 02 §14): a short answer to an action that has no place on the page.
// Success lives 4 s, an error until closed or 8 s; hovering pauses the timer; at most 3;
// the same text is not shown twice — a repeat restarts its timer.
//   const toast = useToast(); toast.ok('Скопировано'); toast.bad('Не удалось изменить автопродление')
const ToastContext = createContext({ ok: () => {}, bad: () => {} })

let seq = 0

export function ToastProvider({ children }) {
  const [items, setItems] = useState([])
  const ref = useRef([])
  const timers = useRef(new Map())

  const commit = useCallback((next) => { ref.current = next; setItems(next) }, [])

  const remove = useCallback((id) => {
    clearTimeout(timers.current.get(id))
    timers.current.delete(id)
    commit(ref.current.map((t) => (t.id === id ? { ...t, leaving: true } : t)))
    setTimeout(() => commit(ref.current.filter((t) => t.id !== id)), 160)
  }, [commit])

  const arm = useCallback((id, ms) => {
    clearTimeout(timers.current.get(id))
    timers.current.set(id, setTimeout(() => remove(id), ms))
  }, [remove])

  const push = useCallback((kind, text) => {
    const ms = kind === 'bad' ? 8000 : 4000
    const same = ref.current.find((t) => t.text === text && t.kind === kind && !t.leaving)
    if (same) { arm(same.id, ms); return }
    const id = ++seq
    const next = [...ref.current.filter((t) => !t.leaving), { id, kind, text, ms }]
    while (next.length > 3) {
      const old = next.shift()
      clearTimeout(timers.current.get(old.id))
      timers.current.delete(old.id)
    }
    commit(next)
    arm(id, ms)
  }, [arm, commit])

  const api = useMemo(() => ({ ok: (t) => push('ok', t), bad: (t) => push('bad', t) }), [push])

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {items.map((t) => (
          <div
            key={t.id}
            className={`toast ${t.kind === 'bad' ? 'toast--bad' : ''} ${t.leaving ? 'is-leaving' : ''}`}
            role={t.kind === 'bad' ? 'alert' : undefined}
            onMouseEnter={() => clearTimeout(timers.current.get(t.id))}
            onMouseLeave={() => { if (!t.leaving) arm(t.id, t.ms) }}
          >
            <span className="toast__ic"><Icon name={t.kind === 'bad' ? 'x' : 'check'} /></span>
            <span>{t.text}</span>
            {t.kind === 'bad' && (
              <button type="button" className="toast__x" aria-label="Закрыть" onClick={() => remove(t.id)}>
                <Icon name="x" />
              </button>
            )}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

export function useToast() {
  return useContext(ToastContext)
}
