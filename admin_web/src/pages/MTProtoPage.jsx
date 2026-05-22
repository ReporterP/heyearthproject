import { useState, useEffect } from 'react'
import { useToast } from '../components/Toast.jsx'

const API = '/api/admin/mtproto'

async function authedFetch(method, path, body) {
  const token = localStorage.getItem('admin_token') || ''
  const init = {
    method,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  }
  if (body !== undefined) init.body = JSON.stringify(body)
  const res = await fetch(path, init)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export default function MTProtoPage() {
  const toast = useToast()
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState({
    enabled: false,
    host: '',
    port: '8443',
    secret: '',
    sponsor_channel: '',
  })

  async function load() {
    setLoading(true)
    try {
      const data = await authedFetch('GET', API)
      setForm({
        enabled: !!data.enabled,
        host: data.host || '',
        port: data.port || '8443',
        secret: data.secret || '',
        sponsor_channel: data.sponsor_channel || '',
      })
    } catch (e) {
      toast.error(e.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  async function save() {
    setSaving(true)
    try {
      await authedFetch('PUT', API, form)
      toast.success('Сохранено. Клиенты увидят обновление через 30 сек.')
    } catch (e) {
      toast.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  async function regenerateSecret() {
    if (!confirm('Сгенерировать новый секрет? Все клиенты потеряют текущий deeplink.')) return
    setSaving(true)
    try {
      const { secret } = await authedFetch('POST', `${API}/generate-secret`)
      setForm((f) => ({ ...f, secret }))
      toast.success('Новый секрет сгенерирован. Нажмите Сохранить, чтобы обновить остальные поля.')
    } catch (e) {
      toast.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <div className="p-6 text-text-secondary">Loading…</div>

  return (
    <div className="p-4 md:p-6 max-w-2xl">
      <h1 className="text-xl md:text-2xl font-bold mb-1">MTProto Telegram Proxy</h1>
      <p className="text-sm text-text-secondary mb-6">
        Кнопка для добавления HeYearth MTProto-прокси в Telegram. Бонус-фича для активных подписчиков.
      </p>

      <div className="space-y-4 bg-surface-card rounded-xl border border-white/10 p-5">
        <label className="flex items-center gap-3 cursor-pointer">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            className="w-4 h-4"
          />
          <span className="text-sm">Включить MTProto (виден ли deeplink в личном кабинете)</span>
        </label>

        <div>
          <label className="text-xs text-text-secondary block mb-1">Host (FQDN или IP exit-VPS)</label>
          <input
            type="text"
            value={form.host}
            onChange={(e) => setForm({ ...form, host: e.target.value })}
            className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm font-mono"
            placeholder="89.124.89.126"
          />
        </div>

        <div>
          <label className="text-xs text-text-secondary block mb-1">Port</label>
          <input
            type="text"
            value={form.port}
            onChange={(e) => setForm({ ...form, port: e.target.value })}
            className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm font-mono"
            placeholder="8443"
          />
        </div>

        <div>
          <label className="text-xs text-text-secondary block mb-1">Secret (32 hex chars)</label>
          <div className="flex gap-2">
            <input
              type="text"
              value={form.secret}
              onChange={(e) => setForm({ ...form, secret: e.target.value })}
              className="flex-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm font-mono"
              placeholder="d4...e1"
            />
            <button
              onClick={regenerateSecret}
              disabled={saving}
              className="px-3 py-2 rounded-lg bg-amber-500/20 text-amber-300 text-xs hover:bg-amber-500/30"
            >
              Regenerate
            </button>
          </div>
        </div>

        <div>
          <label className="text-xs text-text-secondary block mb-1">
            Sponsor channel (необязательно — баннер канала в Telegram)
          </label>
          <input
            type="text"
            value={form.sponsor_channel}
            onChange={(e) => setForm({ ...form, sponsor_channel: e.target.value })}
            className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm"
            placeholder="@hiearth (тег получите у @mtproxybot)"
          />
        </div>

        <div className="pt-2">
          <button
            onClick={save}
            disabled={saving}
            className="px-5 py-2 rounded-lg bg-brand/30 text-brand text-sm font-medium hover:bg-brand/40 disabled:opacity-50"
          >
            {saving ? 'Сохраняем…' : 'Сохранить'}
          </button>
        </div>
      </div>

      <p className="text-xs text-text-secondary mt-4">
        Для тега напишите <code>@mtproxybot</code> команду <code className="text-text-primary">/newproxy</code> с host+port+secret.
      </p>
    </div>
  )
}
