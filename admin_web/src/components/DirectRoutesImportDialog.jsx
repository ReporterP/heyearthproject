import { useEffect, useRef, useState } from 'react'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'
import PlanSelector from './PlanSelector'

/**
 * DirectRoutesImportDialog — bulk JSON import modal.
 *
 * Two-stage UX: 1) configure + dry-run preview, 2) commit real import.
 *
 * Props:
 *   open:        bool
 *   onClose:     () => void
 *   onImported:  () => void   called after a successful non-dry-run import
 */
export default function DirectRoutesImportDialog({ open, onClose, onImported }) {
  const { token } = useAuth()
  const toast = useToast()
  const fileInputRef = useRef(null)
  const [jsonText, setJsonText] = useState('')
  const [fileName, setFileName] = useState('')
  const [overwrite, setOverwrite] = useState(false)
  const [activateNew, setActivateNew] = useState(true)
  const [defaultPlanIDs, setDefaultPlanIDs] = useState([])
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState(null) // ImportResult
  const [dragOver, setDragOver] = useState(false)

  useEffect(() => {
    if (!open) {
      // Reset on close.
      setJsonText('')
      setFileName('')
      setOverwrite(false)
      setActivateNew(true)
      setDefaultPlanIDs([])
      setPreview(null)
      setBusy(false)
      setDragOver(false)
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  async function handleFile(file) {
    if (!file) return
    try {
      const text = await file.text()
      setJsonText(text)
      setFileName(file.name)
      setPreview(null)
    } catch (e) {
      toast.error('Не удалось прочитать файл: ' + e.message)
    }
  }

  function onDrop(e) {
    e.preventDefault()
    setDragOver(false)
    const f = e.dataTransfer?.files?.[0]
    if (f) handleFile(f)
  }

  async function runImport(dryRun) {
    if (!jsonText.trim()) {
      toast.error('Сначала загрузите JSON')
      return
    }
    setBusy(true)
    try {
      const resp = await fetch('/api/admin/direct-routes/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({
          json: jsonText,
          overwrite_existing: overwrite,
          activate_new: activateNew,
          dry_run: dryRun,
          default_plan_ids: defaultPlanIDs,
        }),
      })
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: resp.statusText }))
        throw new Error(err.error || resp.statusText)
      }
      const data = await resp.json()
      if (dryRun) {
        setPreview(data)
        toast.info(`Просмотр: будет создано ${data.created}, обновлено ${data.updated}`)
      } else {
        toast.success(`Импортировано: ${data.created} новых, ${data.updated} обновлено`)
        onImported && onImported()
        onClose()
      }
    } catch (e) {
      toast.error(e.message || 'Ошибка импорта')
    } finally {
      setBusy(false)
    }
  }

  const sizeLabel = jsonText ? `${(jsonText.length / 1024).toFixed(1)} КБ` : ''

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div
        className="w-full max-w-3xl max-h-[90vh] overflow-y-auto rounded-lg border border-slate-700 bg-slate-900 p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-xl font-semibold text-slate-100 mb-4">Импорт JSON</h2>

        {/* Drop zone */}
        <div
          onDragOver={(e) => { e.preventDefault(); setDragOver(true) }}
          onDragLeave={() => setDragOver(false)}
          onDrop={onDrop}
          onClick={() => fileInputRef.current?.click()}
          className={`cursor-pointer rounded-lg border-2 border-dashed p-6 text-center transition ${
            dragOver ? 'border-emerald-500 bg-emerald-900/20' : 'border-slate-600 bg-slate-800/40'
          }`}
        >
          <div className="text-sm text-slate-300">
            {fileName ? (
              <>Загружен: <span className="font-mono text-emerald-300">{fileName}</span> ({sizeLabel})</>
            ) : (
              <>Перетащите файл сюда или нажмите для выбора</>
            )}
          </div>
          <input
            ref={fileInputRef} type="file" accept=".json,application/json"
            className="hidden"
            onChange={(e) => handleFile(e.target.files?.[0])}
          />
        </div>

        {/* Or paste */}
        <details className="mt-3">
          <summary className="cursor-pointer text-sm text-slate-400 hover:text-slate-200">
            …или вставьте JSON вручную
          </summary>
          <textarea
            value={jsonText}
            onChange={(e) => { setJsonText(e.target.value); setFileName(''); setPreview(null) }}
            rows={8}
            placeholder='{ "service-slug": { "name": "...", "group": "russia", "domains": [...], "ip4": [...] } }'
            className="mt-2 w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded text-slate-100 font-mono text-xs"
          />
        </details>

        {/* Options */}
        <div className="mt-4 space-y-2 border-t border-slate-800 pt-4">
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)} />
            <span className="text-sm text-slate-200">Перезаписывать существующие записи (по slug)</span>
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={activateNew} onChange={(e) => setActivateNew(e.target.checked)} />
            <span className="text-sm text-slate-200">Активировать новые записи</span>
          </label>
          <div className="pt-2">
            <div className="text-xs text-slate-400 mb-1">Привязать импортированные записи к планам</div>
            <PlanSelector value={defaultPlanIDs} onChange={setDefaultPlanIDs} />
          </div>
        </div>

        {/* Preview result */}
        {preview && (
          <div className="mt-4 rounded border border-slate-700 bg-slate-800/60 p-3 text-sm">
            <div className="font-semibold text-slate-100 mb-2">Предварительный результат</div>
            <div className="grid grid-cols-4 gap-2 text-center">
              <Stat label="Создать" value={preview.created} tone="emerald" />
              <Stat label="Обновить" value={preview.updated} tone="sky" />
              <Stat label="Пропуск" value={preview.skipped} tone="slate" />
              <Stat label="Ошибок" value={preview.errors?.length || 0} tone={preview.errors?.length > 0 ? 'amber' : 'slate'} />
            </div>
            {preview.warnings?.length > 0 && (
              <details className="mt-2">
                <summary className="text-xs text-slate-400 cursor-pointer">Предупреждения ({preview.warnings.length})</summary>
                <ul className="mt-1 text-xs text-slate-300 list-disc list-inside max-h-32 overflow-y-auto">
                  {preview.warnings.slice(0, 20).map((w, i) => <li key={i}>{w}</li>)}
                </ul>
              </details>
            )}
            {preview.errors?.length > 0 && (
              <details className="mt-2" open>
                <summary className="text-xs text-amber-400 cursor-pointer">Ошибки ({preview.errors.length})</summary>
                <ul className="mt-1 text-xs text-amber-200 list-disc list-inside max-h-32 overflow-y-auto">
                  {preview.errors.slice(0, 10).map((e, i) => (
                    <li key={i}>
                      <span className="font-mono">{e.slug || '?'}</span> · {e.field}: {e.message}
                      {e.value ? ` (${e.value})` : ''}
                    </li>
                  ))}
                </ul>
              </details>
            )}
          </div>
        )}

        {/* Buttons */}
        <div className="flex justify-end gap-2 mt-6 border-t border-slate-800 pt-4">
          <button type="button" onClick={onClose} disabled={busy}
            className="px-4 py-2 rounded bg-slate-700 text-slate-100 hover:bg-slate-600 disabled:opacity-50">
            Отмена
          </button>
          <button type="button" onClick={() => runImport(true)} disabled={busy || !jsonText.trim()}
            className="px-4 py-2 rounded bg-slate-600 text-slate-100 hover:bg-slate-500 disabled:opacity-50">
            Предварительный просмотр
          </button>
          <button type="button" onClick={() => runImport(false)}
            disabled={busy || !jsonText.trim() || !preview}
            className="px-4 py-2 rounded bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-50">
            {busy ? 'Импорт…' : 'Импортировать'}
          </button>
        </div>
      </div>
    </div>
  )
}

function Stat({ label, value, tone }) {
  const toneClass = {
    emerald: 'text-emerald-300',
    sky: 'text-sky-300',
    slate: 'text-slate-300',
    amber: 'text-amber-300',
  }[tone] || 'text-slate-300'
  return (
    <div>
      <div className={`text-xl font-semibold ${toneClass}`}>{value}</div>
      <div className="text-xs text-slate-500">{label}</div>
    </div>
  )
}
