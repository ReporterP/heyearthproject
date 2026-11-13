import { useEffect, useRef, useState } from 'react'
import { discoverDirectRoute, createDirectRoute } from '../api/admin.js'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'

/**
 * DiscoveryDialog — modal for root-domain discovery.
 *
 * Props:
 *   open:       bool        dialog visibility
 *   onClose:    () => void  close handler
 *   onCreated:  () => void  called after the user saves the discovered service
 *
 * Stages:
 *   1. input    — text field + "Найти" button
 *   2. loading  — spinner with rotating message
 *   3. preview  — editable slug/name/group + accordions for domains/ip4/ip6
 *                 + "Создать сервис" button
 */
export default function DiscoveryDialog({ open, onClose, onCreated }) {
  const toast = useToast()

  const [stage, setStage] = useState('input')
  const [rootDomain, setRootDomain] = useState('')
  const [loadingMsg, setLoadingMsg] = useState('Запрос crt.sh…')
  const [result, setResult] = useState(null)
  const [editSlug, setEditSlug] = useState('')
  const [editName, setEditName] = useState('')
  const [editGroup, setEditGroup] = useState('')
  const [editActive, setEditActive] = useState(true)
  const [busy, setBusy] = useState(false)
  const loadingTimerRef = useRef(null)

  useEffect(() => {
    if (!open) {
      setStage('input')
      setRootDomain('')
      setResult(null)
      setEditSlug('')
      setEditName('')
      setEditGroup('')
      setEditActive(true)
      setBusy(false)
      if (loadingTimerRef.current) clearInterval(loadingTimerRef.current)
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  async function runDiscovery() {
    const trimmed = rootDomain.trim().toLowerCase()
    if (!trimmed) {
      toast.error('Введите корневой домен')
      return
    }
    setStage('loading')
    setLoadingMsg('Запрос crt.sh…')
    let n = 0
    loadingTimerRef.current = setInterval(() => {
      n++
      if (n === 1) setLoadingMsg('Получение списка поддоменов…')
      else if (n === 3) setLoadingMsg('DNS-резолвинг…')
      else if (n === 7) setLoadingMsg('Почти готово…')
    }, 1500)
    try {
      let body
      try {
        body = await discoverDirectRoute(trimmed)
      } catch (err) {
        if (err.status === 429) toast.error('Превышен лимит поиска (10 в час), попробуйте через час')
        else if (err.status === 400) toast.error('Неверный домен: ' + err.message)
        else if (err.status === 502) toast.error('Поддомены не найдены, попробуйте другой домен')
        else if (err.status === 504) toast.error('Поиск занял больше 30 секунд')
        else toast.error('Ошибка: ' + err.message)
        setStage('input')
        return
      }
      setResult(body)
      setEditSlug(body.slug || '')
      setEditName(body.name || '')
      setEditGroup(body.group_name || 'discovered')
      setEditActive(true)
      setStage('preview')
    } catch (e) {
      toast.error('Сетевая ошибка: ' + e.message)
      setStage('input')
    } finally {
      if (loadingTimerRef.current) {
        clearInterval(loadingTimerRef.current)
        loadingTimerRef.current = null
      }
    }
  }

  async function saveService() {
    setBusy(true)
    try {
      const payload = {
        slug: editSlug.trim().toLowerCase(),
        name: editName.trim(),
        group_name: editGroup.trim().toLowerCase() || 'discovered',
        source: result.source,
        is_active: editActive,
        domains: result.domains || [],
        ip4: result.ip4 || [],
        ip6: result.ip6 || [],
        cidr4: [],
        cidr6: [],
        dns_servers: [],
        timeout_sec: 3600,
        notes: `Auto-discovered ${result.subdomains_resolved}/${result.subdomains_found} subdomains`,
      }
      let body
      try {
        body = await createDirectRoute(payload)
      } catch (err) {
        const msg = err.message || ''
        if (err.status === 409 || msg.includes('duplicate') || msg.includes('unique')) {
          toast.error(`Сервис со slug=${editSlug} уже существует. Откройте его и добавьте новые домены вручную.`)
        } else {
          toast.error('Ошибка создания: ' + msg)
        }
        return
      }
      toast.success(`Создан сервис id=${body.id} — ${payload.domains.length} доменов, ${payload.ip4.length + payload.ip6.length} IP`)
      onCreated && onCreated()
      onClose()
    } catch (e) {
      toast.error('Сетевая ошибка: ' + e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div
        className="w-full max-w-3xl max-h-[90vh] overflow-y-auto rounded-lg border border-slate-700 bg-slate-900 p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-xl font-semibold text-slate-100 mb-4">Поиск сервиса по домену</h2>

        {stage === 'input' && (
          <div className="space-y-4">
            <div>
              <label className="block text-sm text-slate-300 mb-1">Корневой домен</label>
              <input
                type="text" value={rootDomain}
                onChange={(e) => setRootDomain(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') runDiscovery() }}
                placeholder="prima-inform.ru"
                className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded text-slate-100"
                autoFocus
              />
              <p className="text-xs text-slate-400 mt-1">
                Найдёт все поддомены через crt.sh и IP-адреса через DNS. Займёт ~10 секунд.
              </p>
            </div>
            <div className="flex justify-end gap-2">
              <button type="button" onClick={onClose}
                className="px-4 py-2 rounded bg-slate-700 text-slate-100 hover:bg-slate-600">Отмена</button>
              <button type="button" onClick={runDiscovery}
                className="px-4 py-2 rounded bg-purple-600 text-white hover:bg-purple-500">Найти</button>
            </div>
          </div>
        )}

        {stage === 'loading' && (
          <div className="py-12 flex flex-col items-center gap-4">
            <div className="h-8 w-8 border-2 border-purple-400 border-t-transparent rounded-full animate-spin"></div>
            <div className="text-slate-300 text-sm">{loadingMsg}</div>
          </div>
        )}

        {stage === 'preview' && result && (
          <div className="space-y-3">
            <div className="rounded border border-slate-700 bg-slate-800/60 p-3 text-sm">
              <div className="text-slate-100 font-medium">
                {result.subdomains_found} поддоменов · {result.ip4.length} IPv4 · {result.ip6.length} IPv6 · {result.duration_ms}мс
              </div>
              {result.subdomains_found > result.subdomains_resolved && (
                <div className="text-xs text-amber-300 mt-1">
                  {result.subdomains_found - result.subdomains_resolved} поддоменов не зарезолвились
                </div>
              )}
              {result.warnings && result.warnings.length > 0 && (
                <details className="mt-2 text-xs text-amber-200">
                  <summary className="cursor-pointer">Предупреждения ({result.warnings.length})</summary>
                  <ul className="mt-1 list-disc list-inside">
                    {result.warnings.map((w, i) => <li key={i}>{w}</li>)}
                  </ul>
                </details>
              )}
            </div>

            <div>
              <label className="block text-xs text-slate-400 mb-1">Slug</label>
              <input value={editSlug} onChange={(e) => setEditSlug(e.target.value)}
                className="w-full px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100" />
            </div>
            <div>
              <label className="block text-xs text-slate-400 mb-1">Название</label>
              <input value={editName} onChange={(e) => setEditName(e.target.value)}
                className="w-full px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100" />
            </div>
            <div>
              <label className="block text-xs text-slate-400 mb-1">Группа</label>
              <input value={editGroup} onChange={(e) => setEditGroup(e.target.value)}
                className="w-full px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100" />
            </div>
            <label className="inline-flex items-center gap-2">
              <input type="checkbox" checked={editActive} onChange={(e) => setEditActive(e.target.checked)} />
              <span className="text-sm text-slate-300">Активен</span>
            </label>

            <details className="rounded border border-slate-700 bg-slate-800/40">
              <summary className="cursor-pointer px-3 py-2 text-sm text-slate-200">
                Домены ({result.domains.length})
              </summary>
              <pre className="px-3 py-2 max-h-48 overflow-y-auto text-xs font-mono text-slate-300">{result.domains.join('\n')}</pre>
            </details>
            <details className="rounded border border-slate-700 bg-slate-800/40">
              <summary className="cursor-pointer px-3 py-2 text-sm text-slate-200">
                IPv4 ({result.ip4.length})
              </summary>
              <pre className="px-3 py-2 max-h-48 overflow-y-auto text-xs font-mono text-slate-300">{result.ip4.join('\n')}</pre>
            </details>
            <details className="rounded border border-slate-700 bg-slate-800/40">
              <summary className="cursor-pointer px-3 py-2 text-sm text-slate-200">
                IPv6 ({result.ip6.length})
              </summary>
              <pre className="px-3 py-2 max-h-48 overflow-y-auto text-xs font-mono text-slate-300">{result.ip6.join('\n')}</pre>
            </details>

            <div className="flex justify-end gap-2 pt-4 border-t border-slate-800">
              <button type="button" onClick={onClose} disabled={busy}
                className="px-4 py-2 rounded bg-slate-700 text-slate-100 hover:bg-slate-600 disabled:opacity-50">Отмена</button>
              <button type="button" onClick={saveService} disabled={busy}
                className="px-4 py-2 rounded bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-50">
                {busy ? 'Сохранение…' : 'Создать сервис'}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
