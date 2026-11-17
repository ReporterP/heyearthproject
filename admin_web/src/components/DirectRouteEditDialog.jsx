import { useEffect, useState } from 'react'
import { Dialog } from './ui/index.js'
import { createDirectRoute, updateDirectRoute } from '../api/admin.js'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'

/**
 * DirectRouteEditDialog — modal CRUD form for a direct routing service.
 *
 * Props:
 *   open:     bool           dialog visibility
 *   service:  object | null  existing service for edit, null for create
 *   onClose:  () => void
 *   onSaved:  () => void     called after successful save (parent should refresh list)
 */
export default function DirectRouteEditDialog({ open, service, onClose, onSaved }) {
  const toast = useToast()
  const isEdit = Boolean(service && service.id)

  const [form, setForm] = useState(blankForm())
  const [submitting, setSubmitting] = useState(false)
  const [errors, setErrors] = useState({})

  // (Re)initialise when dialog opens or service changes.
  useEffect(() => {
    if (!open) return
    if (service) {
      setForm({
        slug: service.slug || '',
        name: service.name || '',
        group_name: service.group_name || 'other',
        source: service.source || '',
        is_active: service.is_active ?? true,
        domains: arrayToLines(service.domains),
        ip4: arrayToLines(service.ip4),
        ip6: arrayToLines(service.ip6),
        cidr4: arrayToLines(service.cidr4),
        cidr6: arrayToLines(service.cidr6),
        dns_servers: arrayToLines(service.dns_servers),
        timeout_sec: service.timeout_sec || 3600,
        notes: service.notes || '',
      })
    } else {
      setForm(blankForm())
    }
    setErrors({})
  }, [open, service]) // eslint-disable-line react-hooks/exhaustive-deps


  if (!open) return null

  function update(field, val) {
    setForm((f) => ({ ...f, [field]: val }))
  }

  function validate() {
    const errs = {}
    const slug = (form.slug || '').trim().toLowerCase()
    if (!slug) errs.slug = 'Обязательно'
    else if (!/^[a-z0-9._:@-]+$/.test(slug)) errs.slug = 'Только a-z, 0-9, . _ : @ -'
    if (!(form.name || '').trim()) errs.name = 'Обязательно'
    const t = parseInt(form.timeout_sec, 10)
    if (Number.isNaN(t) || t < 60 || t > 86400) errs.timeout_sec = '60..86400'
    // Light per-line checks (don't block — backend will sanitise). TLD allows Punycode (xn--).
    const badDomains = linesToArray(form.domains).filter((d) => !/^[a-z0-9.*-]+\.[a-z][a-z0-9-]{1,}$/i.test(d))
    if (badDomains.length > 0) errs.domains = `Не валидно: ${badDomains.slice(0, 3).join(', ')}`
    setErrors(errs)
    return Object.keys(errs).length === 0
  }

  async function handleSubmit(e) {
    e.preventDefault()
    if (!validate()) return
    setSubmitting(true)
    try {
      const payload = {
        slug: form.slug.trim().toLowerCase(),
        name: form.name.trim(),
        group_name: (form.group_name || 'other').trim().toLowerCase(),
        source: (form.source || '').trim(),
        is_active: !!form.is_active,
        domains: linesToArray(form.domains),
        ip4: linesToArray(form.ip4),
        ip6: linesToArray(form.ip6),
        cidr4: linesToArray(form.cidr4),
        cidr6: linesToArray(form.cidr6),
        dns_servers: linesToArray(form.dns_servers),
        timeout_sec: parseInt(form.timeout_sec, 10) || 3600,
        notes: form.notes || '',
      }
      let savedID = service?.id
      if (isEdit) {
        await updateDirectRoute(service.id, payload)
      } else {
        const data = await createDirectRoute(payload)
        savedID = data.id
      }
      toast.success(isEdit ? 'Сохранено' : `Создано (id=${savedID})`)
      onSaved && onSaved()
      onClose()
    } catch (e) {
      toast.error(e.message || 'Ошибка сохранения')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open onClose={onClose} title={isEdit ? `Изменить: ${service.name || service.slug}` : 'Новый сервис прямой маршрутизации'} maxWidth="max-w-3xl">

      <form onSubmit={handleSubmit} className="space-y-3">
        <Row label="Slug" error={errors.slug} hint="a-z 0-9 . -">
          <input
            type="text" value={form.slug} onChange={(e) => update('slug', e.target.value)}
            className="w-full px-3 py-1.5 bg-canvas border border-default rounded text-primary"
            disabled={isEdit}
          />
        </Row>

        <Row label="Название" error={errors.name}>
          <input
            type="text" value={form.name} onChange={(e) => update('name', e.target.value)}
            className="w-full px-3 py-1.5 bg-canvas border border-default rounded text-primary"
          />
        </Row>

        <div className="grid grid-cols-2 gap-3">
          <Row label="Группа">
            <input
              type="text" value={form.group_name} onChange={(e) => update('group_name', e.target.value)}
              placeholder="russia / vk / yandex / other"
              className="w-full px-3 py-1.5 bg-canvas border border-default rounded text-primary"
            />
          </Row>
          <Row label="Источник">
            <input
              type="text" value={form.source} onChange={(e) => update('source', e.target.value)}
              placeholder="URL или метка"
              className="w-full px-3 py-1.5 bg-canvas border border-default rounded text-primary"
            />
          </Row>
        </div>

        <Row label="Активен">
          <label className="inline-flex items-center gap-2">
            <input type="checkbox" checked={!!form.is_active} onChange={(e) => update('is_active', e.target.checked)} />
            <span className="text-sm text-secondary">да</span>
          </label>
        </Row>

        <TextareaRow label="Домены (по одному в строке)" value={form.domains} onChange={(v) => update('domains', v)} error={errors.domains} rows={5} />
        <div className="grid grid-cols-2 gap-3">
          <TextareaRow label="IPv4 (по одному)" value={form.ip4} onChange={(v) => update('ip4', v)} rows={4} />
          <TextareaRow label="IPv6 (по одному)" value={form.ip6} onChange={(v) => update('ip6', v)} rows={4} />
          <TextareaRow label="CIDR v4" value={form.cidr4} onChange={(v) => update('cidr4', v)} rows={3} />
          <TextareaRow label="CIDR v6" value={form.cidr6} onChange={(v) => update('cidr6', v)} rows={3} />
        </div>
        <TextareaRow label="DNS серверы (опционально)" value={form.dns_servers} onChange={(v) => update('dns_servers', v)} rows={2} />

        <div className="grid grid-cols-2 gap-3">
          <Row label="Timeout (сек)" error={errors.timeout_sec}>
            <input
              type="number" min={60} max={86400}
              value={form.timeout_sec} onChange={(e) => update('timeout_sec', e.target.value)}
              className="w-full px-3 py-1.5 bg-canvas border border-default rounded text-primary"
            />
          </Row>
          <Row label="Заметки">
            <input
              type="text" value={form.notes} onChange={(e) => update('notes', e.target.value)}
              className="w-full px-3 py-1.5 bg-canvas border border-default rounded text-primary"
            />
          </Row>
        </div>

        <div className="flex justify-end gap-2 pt-4 border-t border-default">
          <button type="button" onClick={onClose}
            className="px-4 py-2 rounded bg-hover text-primary hover:bg-hover">
            Отмена
          </button>
          <button type="submit" disabled={submitting}
            className="px-4 py-2 rounded bg-success text-white hover:opacity-90 disabled:opacity-50">
            {submitting ? 'Сохранение…' : (isEdit ? 'Сохранить' : 'Создать')}
          </button>
        </div>
      </form>
    </Dialog>
  )
}

function blankForm() {
  return {
    slug: '', name: '', group_name: 'other', source: '',
    is_active: true,
    domains: '', ip4: '', ip6: '', cidr4: '', cidr6: '', dns_servers: '',
    timeout_sec: 3600, notes: '',
  }
}

function arrayToLines(arr) { return Array.isArray(arr) ? arr.join('\n') : '' }
function linesToArray(s) {
  return (s || '').split('\n').map((x) => x.trim()).filter(Boolean)
}

function Row({ label, error, hint, children }) {
  return (
    <div>
      <div className="flex justify-between text-xs text-secondary mb-1">
        <span>{label}</span>
        {hint && <span className="text-tertiary">{hint}</span>}
      </div>
      {children}
      {error && <div className="text-xs text-danger mt-1">{error}</div>}
    </div>
  )
}

function TextareaRow({ label, value, onChange, error, rows = 4 }) {
  return (
    <Row label={label} error={error}>
      <textarea
        value={value} onChange={(e) => onChange(e.target.value)}
        rows={rows}
        className="w-full px-3 py-1.5 bg-canvas border border-default rounded text-primary font-mono text-sm"
      />
    </Row>
  )
}
