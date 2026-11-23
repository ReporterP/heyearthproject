import { useEffect, useState } from 'react'
import { useToast } from '../Toast.jsx'
import { getSettings, updateSetting } from '../../api/admin.js'
import { Button, Card, CardHeader, CardBody, Input, Skeleton } from '../ui/index.js'

// The second channel and the watchdog outside the Hub. The pulse address and the watcher bot's
// token are written here and never read back: the page only learns that they are set.
const SECRETS = [
  { key: 'watchdog_api_pulse_url', label: 'Адрес пульса API', placeholder: 'https://…',
    hint: 'Адрес проверки во внешнем стороже (например, healthchecks.io). API открывает его раз в минуту; когда пульс пропадает, сторож пишет сам, своим каналом.' },
  { key: 'watchdog_bot_token', label: 'Токен бота-сторожа', placeholder: '123456789:AA…',
    hint: 'Отдельный бот из @BotFather, добавленный в чат тревог. Его токен лежит на выходах: сторож на выходе пишет в чат напрямую, когда хаб не открывается снаружи.' },
]

export default function WatchdogSettings({ canWrite }) {
  const toast = useToast()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [email, setEmail] = useState('')
  const [savedEmail, setSavedEmail] = useState('')
  const [isSet, setIsSet] = useState({})
  const [values, setValues] = useState({})
  const [saving, setSaving] = useState('')

  async function load() {
    try {
      const data = await getSettings()
      const mail = data.settings?.alert_email || ''
      setEmail(mail)
      setSavedEmail(mail)
      setIsSet(data.secrets_set || {})
      setError('')
    } catch (e) { setError(e.message) } finally { setLoading(false) }
  }
  useEffect(() => { load() }, [])

  async function save(key, value, okText) {
    setSaving(key)
    try {
      await updateSetting(key, value)
      toast.success(okText)
      setValues(v => ({ ...v, [key]: '' }))
      await load()
    } catch (e) { toast.error(e.message) } finally { setSaving('') }
  }

  return (
    <Card className="max-w-2xl">
      <CardHeader><h3 className="text-sm font-semibold text-primary">Второй канал и сторож</h3></CardHeader>
      <CardBody className="space-y-4">
        {loading ? <Skeleton className="h-24 w-full" /> : (<>
          {error && <p role="alert" className="text-xs text-danger">Не удалось загрузить настройки: {error}. Поля заблокированы, чтобы не затереть сохранённое.</p>}
          <div>
            <label htmlFor="alert-email" className="text-xs text-tertiary block mb-1">Адрес для писем</label>
            <div className="flex flex-col sm:flex-row gap-2">
              <Input id="alert-email" type="email" autoComplete="off" value={email} onChange={e => setEmail(e.target.value)}
                placeholder="owner@example.com" aria-describedby="alert-email-hint" className="w-full sm:flex-1" disabled={!canWrite || !!error} />
              {canWrite && (
                <Button variant="secondary" onClick={() => save('alert_email', email.trim(), email.trim() ? 'Адрес сохранён' : 'Письма выключены')}
                  loading={saving === 'alert_email'} disabled={!!error || email.trim() === savedEmail}>Сохранить</Button>
              )}
            </div>
            <p id="alert-email-hint" className="text-xs text-tertiary mt-1">Если тревога не доходит до Telegram 15 минут, она уходит письмом — один раз на инцидент. Пусто — писем нет.</p>
          </div>
          {SECRETS.map((s) => (
            <div key={s.key}>
              <label htmlFor={`ops-${s.key}`} className="text-xs text-tertiary block mb-1">{s.label}</label>
              <div className="flex flex-col sm:flex-row gap-2">
                <Input id={`ops-${s.key}`} type="password" autoComplete="off" value={values[s.key] || ''}
                  onChange={e => setValues(v => ({ ...v, [s.key]: e.target.value }))}
                  placeholder={isSet[s.key] ? 'Задан — введите новое значение, чтобы заменить' : s.placeholder}
                  aria-describedby={`ops-${s.key}-hint`} className="w-full sm:flex-1 font-mono" disabled={!canWrite || !!error} />
                {canWrite && (
                  <Button variant="secondary" onClick={() => save(s.key, values[s.key] || '', 'Сохранено')}
                    loading={saving === s.key} disabled={!!error || !(values[s.key] || '').trim()}>Сохранить</Button>
                )}
              </div>
              <p id={`ops-${s.key}-hint`} className="text-xs text-tertiary mt-1">{s.hint} Значение хранится на сервере и в админку не возвращается.</p>
            </div>
          ))}
          <p className="text-xs text-tertiary">
            Пульс самого хаба ставится из командной строки (<span className="font-mono">deploy --setup-watchdog</span>), сторож на выходе — кнопкой «Поставить сторожа» на выходе.
            Что делать, когда хаб недоступен и пульта нет: <span className="font-mono">docs/runbooks/hub-down.md</span>.
          </p>
        </>)}
      </CardBody>
    </Card>
  )
}
