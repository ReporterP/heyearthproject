import { useEffect, useId, useRef, useState } from 'react'
import { TriangleAlert } from 'lucide-react'
import { Dialog } from './Dialog'
import { Button } from './Button'
import { Input } from './Input'
import { confirmMatches } from './confirm.js'
import { escapeAction } from './a11y.js'
import { useFocusTrap } from './useFocusTrap.js'

/**
 * ConfirmDanger — an irreversible action behind a list of consequences + typed confirmation.
 * Props: open, onClose, title, consequences (string[]), confirmText (what must be typed, e.g. the node name),
 *        actionLabel, onConfirm (async; a throw keeps the dialog open and shows the error inside).
 * The action button is enabled only when confirmMatches(typed, confirmText). Enter submits once it matches.
 * While onConfirm runs, Escape/backdrop/«Отмена» do nothing and a second submit is ignored. Escape is
 * swallowed here, so a Drawer underneath (e.g. NodeDrawer) does not close with it. Focus is trapped inside
 * and returns on close (alertdialog semantics live here: the base Dialog renders no role and no trap).
 */
export function ConfirmDanger({ open, onClose, title, consequences = [], confirmText, actionLabel, onConfirm }) {
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const uid = useId()
  const rootRef = useRef(null)
  const inputRef = useRef(null)
  // The in-flight guard is a ref: state updates land a render later, a second Enter/click would slip through.
  const busyRef = useRef(false)
  // A fresh form on every opening — unless the previous action is still running (keep its state and guard).
  useEffect(() => { if (open && !busyRef.current) { setTyped(''); setError('') } }, [open])

  const close = () => { if (escapeAction(busyRef.current) === 'close') onClose() }
  useFocusTrap(rootRef, open, { initialFocus: inputRef, onEscape: close })

  const matches = confirmMatches(typed, confirmText)
  const ids = { title: `${uid}-title`, body: `${uid}-body`, input: `${uid}-input`, error: `${uid}-error` }

  async function run(e) {
    e.preventDefault()
    if (busyRef.current || !matches) return
    busyRef.current = true
    setBusy(true)
    setError('')
    try {
      await onConfirm()
      onClose()
    } catch (err) {
      setError(err?.message || 'Не удалось выполнить действие')
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onClose={close} maxWidth="max-w-lg">
      <div ref={rootRef} role="alertdialog" aria-modal="true" aria-labelledby={ids.title}
        aria-describedby={consequences.length > 0 ? ids.body : undefined}>
        <div className="flex items-center gap-2 mb-4">
          <TriangleAlert className="w-5 h-5 text-danger flex-shrink-0" aria-hidden="true" />
          <h2 id={ids.title} className="text-lg font-semibold text-primary tracking-tight">{title}</h2>
        </div>
        <form onSubmit={run} noValidate>
          {consequences.length > 0 && (
            <ul id={ids.body} className="list-disc pl-5 space-y-1.5 mb-4 text-sm text-secondary">
              {consequences.map((c, i) => <li key={i}>{c}</li>)}
            </ul>
          )}
          <label className="block text-xs text-tertiary mb-1" htmlFor={ids.input}>
            Введите <span className="font-mono text-primary break-all">{confirmText}</span>, чтобы подтвердить
          </label>
          <Input ref={inputRef} id={ids.input} value={typed} onChange={(e) => setTyped(e.target.value)}
            autoComplete="off" autoCapitalize="none" autoCorrect="off" spellCheck={false} readOnly={busy}
            aria-invalid={error ? true : undefined} aria-describedby={error ? ids.error : undefined} />
          {error && <p id={ids.error} role="alert" className="mt-2 text-xs text-danger break-words">{error}</p>}
          <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2 mt-5">
            <Button type="button" variant="secondary" onClick={close} disabled={busy}>Отмена</Button>
            <Button type="submit" variant="danger" disabled={!matches} loading={busy}>{actionLabel}</Button>
          </div>
        </form>
      </div>
    </Dialog>
  )
}
