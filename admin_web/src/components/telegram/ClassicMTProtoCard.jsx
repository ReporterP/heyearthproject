import { useCallback, useEffect, useId, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { fetchMTProto, saveMTProto, generateMTProtoSecret } from '../../api/admin.js'
import { Button, Card, CardBody, CardHeader, Dialog, Input, Skeleton, Switch } from '../ui/index.js'

const EMPTY = { enabled: false, secret: '', fronting_domain: '', sponsor_channel: '', auto_install: true }

function formFrom(d) {
  return {
    enabled: !!d?.enabled, secret: d?.secret || '', fronting_domain: d?.fronting_domain || '',
    sponsor_channel: d?.sponsor_channel || '', auto_install: d?.auto_install !== false,
  }
}

function Header() {
  return (
    <CardHeader>
      <h2 className="text-sm font-semibold text-primary">Классический MTProto</h2>
      <p className="text-xs text-tertiary mt-0.5">Запасной: iOS и старые версии Telegram</p>
    </CardHeader>
  )
}

/**
 * ClassicMTProtoCard — the classic Telegram proxy (mtg with fake-TLS on the exits) for subscribers (a button
 * in the cabinet). «Сгенерировать» only fills the field: the new secret takes effect on «Сохранить», after a
 * warning that every client's current link stops working. Switched off, MTProto is put on no exit (C6).
 * Moved as is from MTProtoPage; the WEB proxy above it is the primary Telegram path now.
 */
export default function ClassicMTProtoCard() {
  const { can } = useAuth()
  const toast = useToast()
  const uid = useId()
  const canEdit = can('settings.write')
  const [saved, setSaved] = useState(null)
  const [form, setForm] = useState(EMPTY)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [confirmSecret, setConfirmSecret] = useState(false)
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))

  const load = useCallback(async () => {
    try {
      const d = await fetchMTProto()
      setSaved(formFrom(d))
      setForm(formFrom(d))
      setError('')
    } catch (e) {
      setError(e?.message || 'ошибка')
    }
  }, [])
  useEffect(() => { load() }, [load])

  async function generate() {
    setBusy('generate')
    try {
      const { secret } = await generateMTProtoSecret(form.fronting_domain.trim())
      set({ secret })
      toast.info('Новый секрет в поле — он заработает после «Сохранить»')
    } catch (e) {
      toast.error(e.message)
    } finally {
      setBusy('')
    }
  }

  async function save() {
    setBusy('save')
    try {
      await saveMTProto({ ...form, fronting_domain: form.fronting_domain.trim(), secret: form.secret.trim() })
      toast.success('Сохранено — кабинеты и подписки обновятся в течение минуты')
      setConfirmSecret(false)
      await load()
    } catch (e) {
      toast.error(e.message)
    } finally {
      setBusy('')
    }
  }
  const onSave = () => (saved && saved.secret && form.secret.trim() !== saved.secret ? setConfirmSecret(true) : save())

  if (!saved && !error) {
    return (
      <Card>
        <Header />
        <CardBody className="space-y-3"><Skeleton className="h-10" /><Skeleton className="h-64" /></CardBody>
      </Card>
    )
  }
  if (!saved) {
    return (
      <div role="alert" className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
        <span className="text-sm text-danger">Не удалось загрузить MTProto: {error}</span>
        <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Повторить</Button>
      </div>
    )
  }
  const dirty = JSON.stringify(form) !== JSON.stringify(saved)
  return (
    <Card>
      <Header />
      <CardBody className="space-y-5">
        <p className="text-xs text-secondary">
          Кнопка «Добавить прокси в Telegram» в кабинете подписчика. Адрес выбирается сам — здоровый выход при каждом запросе.
        </p>
        <Switch checked={form.enabled} disabled={!canEdit} onChange={(v) => set({ enabled: v })} label="Включить"
          description="Выключенный: кнопки в кабинете нет, на выходы прокси не ставится (ни при настройке, ни автоматически)." />
        <Switch checked={form.auto_install} disabled={!canEdit || !form.enabled} onChange={(v) => set({ auto_install: v })}
          label="Ставить на выходы автоматически"
          description="Раз в 30 минут на один выход без прокси (только пока MTProto включён)." />

        <div>
          <label htmlFor={`${uid}-d`} className="text-xs text-secondary block mb-1">Домен маскировки (fake-TLS)</label>
          <Input id={`${uid}-d`} value={form.fronting_domain} disabled={!canEdit} onChange={(e) => set({ fronting_domain: e.target.value })}
            className="font-mono" placeholder="mt.example.ru" />
          <p className="text-xs text-tertiary mt-1">
            A-запись домена <span className="text-secondary">обязана</span> указывать на выходы: Telegram подменяет адрес назначения
            этим доменом — если он ведёт не на выход, прокси недоступен, хотя все проверки на сервере зелёные. Сменили домен —
            сгенерируйте секрет заново.
          </p>
        </div>

        <div>
          <label htmlFor={`${uid}-s`} className="text-xs text-secondary block mb-1">Секрет</label>
          <div className="flex flex-col sm:flex-row gap-2">
            <Input id={`${uid}-s`} value={form.secret} disabled={!canEdit} onChange={(e) => set({ secret: e.target.value })}
              className="font-mono w-full sm:flex-1" placeholder="ee…" />
            {canEdit && (
              <Button variant="secondary" onClick={generate} loading={busy === 'generate'} disabled={!!busy || !form.fronting_domain.trim()}
                className="w-full sm:w-auto">Сгенерировать</Button>
            )}
          </div>
          <p className="text-xs text-tertiary mt-1">Формат mtg: ee + 32 hex + домен в hex. «Сгенерировать» только заполняет поле.</p>
        </div>

        <div>
          <label htmlFor={`${uid}-c`} className="text-xs text-secondary block mb-1">Канал-спонсор (необязательно)</label>
          <Input id={`${uid}-c`} value={form.sponsor_channel} disabled={!canEdit} onChange={(e) => set({ sponsor_channel: e.target.value })}
            placeholder="@channel" />
          <p className="text-xs text-tertiary mt-1">Тег выдаёт @mtproxybot по команде /newproxy (адрес выхода, порт {saved.port || '8443'}, секрет).</p>
        </div>

        {canEdit && (
          <div className="flex flex-col sm:flex-row gap-2">
            <Button onClick={onSave} loading={busy === 'save'} disabled={!dirty || !!busy}>Сохранить</Button>
            {dirty && <Button variant="ghost" onClick={() => setForm(saved)}>Отменить</Button>}
          </div>
        )}
      </CardBody>

      <Dialog open={confirmSecret} onClose={() => setConfirmSecret(false)} title="Сменить секрет?" maxWidth="max-w-md">
        <div className="space-y-3 text-sm">
          <p className="text-secondary">Прокси, добавленные в Telegram со старым секретом, перестанут работать: подписчикам придётся
            добавить прокси заново кнопкой в кабинете. Выходы получат новый секрет только после переустановки прокси: в карточке каждого выхода — «Переустановить Telegram-прокси».</p>
          <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2">
            <Button variant="ghost" onClick={() => setConfirmSecret(false)}>Отмена</Button>
            <Button variant="danger" loading={busy === 'save'} onClick={save}>Сменить и сохранить</Button>
          </div>
        </div>
      </Dialog>
    </Card>
  )
}
