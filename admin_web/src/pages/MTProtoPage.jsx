import { useState, useEffect } from 'react'
import { useToast } from '../components/Toast.jsx'
import {
  Button,
  Card,
  Input,
  PageHeader,
  Spinner,
} from '../components/ui/index.js'

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
    secret: '',
    sponsor_channel: '',
    auto_install: true,
  })

  async function load() {
    setLoading(true)
    try {
      const data = await authedFetch('GET', API)
      setForm({
        enabled: !!data.enabled,
        secret: data.secret || '',
        sponsor_channel: data.sponsor_channel || '',
        auto_install: data.auto_install !== false,
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

  if (loading) return (
    <div className="flex justify-center py-20">
      <Spinner size="md" />
    </div>
  )

  return (
    <div className="max-w-2xl space-y-4">
      <PageHeader
        title="MTProto Telegram Proxy"
        subtitle="Кнопка для добавления HeYearth MTProto-прокси в Telegram. Бонус-фича для активных подписчиков."
      />
      <p className="text-xs text-tertiary">
        Host и Port теперь управляются автоматически — балансировщик выбирает здоровый exit-сервер при каждом запросе пользователя.
      </p>

      <Card className="p-5 space-y-4">
        <label className="flex items-center gap-3 cursor-pointer">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            className="w-4 h-4 rounded"
          />
          <span className="text-sm text-primary">Включить MTProto (виден ли deeplink в личном кабинете)</span>
        </label>

        <label className="flex items-center gap-3 cursor-pointer">
          <input
            type="checkbox"
            checked={form.auto_install}
            onChange={(e) => setForm({ ...form, auto_install: e.target.checked })}
            className="w-4 h-4 rounded"
          />
          <span className="text-sm text-primary">Автоматически ставить mtproxy на новые/существующие exit-серверы</span>
        </label>

        <div>
          <label className="text-xs text-tertiary block mb-1">Secret (32 hex chars)</label>
          <div className="flex gap-2">
            <Input
              type="text"
              value={form.secret}
              onChange={(e) => setForm({ ...form, secret: e.target.value })}
              className="font-mono"
              placeholder="d4...e1"
            />
            <Button
              variant="secondary"
              onClick={regenerateSecret}
              disabled={saving}
            >
              Regenerate
            </Button>
          </div>
        </div>

        <div>
          <label className="text-xs text-tertiary block mb-1">
            Sponsor channel (необязательно — баннер канала в Telegram)
          </label>
          <Input
            type="text"
            value={form.sponsor_channel}
            onChange={(e) => setForm({ ...form, sponsor_channel: e.target.value })}
            placeholder="@hiearth (тег получите у @mtproxybot)"
          />
        </div>

        <div className="pt-2">
          <Button
            variant="primary"
            onClick={save}
            loading={saving}
          >
            Сохранить
          </Button>
        </div>
      </Card>

      <p className="text-xs text-tertiary">
        Для тега напишите <code className="text-primary">@mtproxybot</code> команду <code className="text-primary">/newproxy</code> с host+port+secret.
      </p>
    </div>
  )
}
