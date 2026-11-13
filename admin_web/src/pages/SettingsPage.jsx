import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { getSettings, updateSetting } from '../api/admin.js'
import SecretSettingField from '../components/settings/SecretSettingField.jsx'
import SettingField from '../components/settings/SettingField.jsx'
import { SETTINGS_SECTIONS } from '../components/settings/settingsModel.js'
import { Button, Card, CardHeader, CardBody, PageHeader, Skeleton } from '../components/ui/index.js'

// A setting shown but not edited here (its own section owns it).
function ReadOnlySetting({ label, hint, value, empty, emptyWarning = false }) {
  const isEmpty = !String(value ?? '').trim()
  return (
    <div className="py-3">
      <p className="text-sm text-primary mb-1">{label}</p>
      {hint && <p className="text-xs text-tertiary mb-2">{hint}</p>}
      {isEmpty
        ? <p className={`text-sm ${emptyWarning ? 'text-warning' : 'text-secondary'}`}>{empty}</p>
        : <p className="font-mono text-sm text-secondary break-all">{value}</p>}
    </div>
  )
}

function Section({ title, children }) {
  return (
    <Card>
      <CardHeader><h3 className="text-sm font-semibold text-primary">{title}</h3></CardHeader>
      <CardBody className="divide-y divide-[var(--color-border-default)] py-1">{children}</CardBody>
    </Card>
  )
}

/**
 * SettingsPage — «Настройки»: the account and the platform's settings in blocks (C9). Only the
 * keys of settingsModel.js are written; the API checks every value and refuses any other key.
 * MTProto, alerts, entries and the Hub's numbers have their own pages.
 */
export default function SettingsPage() {
  const { t, locale, setLocale } = useT()
  const { admin, logout, can } = useAuth()
  const toast = useToast()
  const canRead = can('settings.read')
  const canEdit = can('settings.write')
  const [settings, setSettings] = useState(null)
  const [secretsSet, setSecretsSet] = useState({})
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const data = await getSettings()
      setSettings(data?.settings || {})
      setSecretsSet(data?.secrets_set || {})
      setError('')
    } catch (e) {
      setError(e?.message || 'ошибка')
    }
  }, [])
  useEffect(() => { if (canRead) load() }, [canRead, load])

  async function saveSetting(key, value) {
    await updateSetting(key, value)
    setSettings((prev) => ({ ...prev, [key]: value }))
    toast.success('Сохранено')
  }
  // Secrets are write-only: only the «set» flag comes back.
  async function saveSecret(key, value) {
    try {
      await updateSetting(key, value)
      setSecretsSet((prev) => ({ ...prev, [key]: true }))
      toast.success('Секрет сохранён')
    } catch (err) {
      toast.error(err.message)
      throw err
    }
  }

  return (
    <div className="max-w-3xl">
      <PageHeader title="Настройки" subtitle="Аккаунт и настройки платформы. Изменения действуют сразу и пишутся в журнал аудита." />
      <div className="space-y-6">
        <Section title="Аккаунт">
          <div className="py-3 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
            <div className="min-w-0">
              <p className="font-mono text-sm text-primary">{admin?.username}</p>
              {admin?.display_name && <p className="text-xs text-tertiary">{admin.display_name}</p>}
            </div>
            <Button variant="danger" onClick={logout} className="w-full sm:w-auto">{t.settingsLogout}</Button>
          </div>
          <div className="py-3 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
            <span className="text-sm text-primary">Язык интерфейса</span>
            <div className="flex gap-2">
              {['ru', 'en'].map((l) => (
                <Button key={l} size="sm" variant={locale === l ? 'secondary' : 'ghost'} aria-pressed={locale === l} onClick={() => setLocale(l)}>
                  {l === 'ru' ? 'Русский' : 'English'}
                </Button>
              ))}
            </div>
          </div>
        </Section>

        {!canRead ? null : error ? (
          <p role="alert" className="text-sm text-danger">Не удалось загрузить настройки: {error}</p>
        ) : !settings ? (
          <div className="space-y-3"><Skeleton className="h-40" /><Skeleton className="h-40" /></div>
        ) : (
          <>
            {SETTINGS_SECTIONS.map((sec) => (
              <Section key={sec.key} title={sec.title}>
                {sec.fields.map((f) => (f.kind === 'secret' ? (
                  <SecretSettingField key={f.key} id={`setting-${f.key}`} label={f.label} hint={f.hint} isSet={secretsSet[f.key]}
                    disabledReason={canEdit ? '' : 'Нужно право «Настройки: изменение»'} onSave={(v) => saveSecret(f.key, v)} />
                ) : (
                  <SettingField key={f.key} field={f} settings={settings} canEdit={canEdit} onSave={saveSetting} />
                )))}
                {sec.key === 'connection' && (
                  <>
                    <ReadOnlySetting label="Адрес входа в профилях (main_server_host)"
                      hint="Куда подключаются приложения пользователей. Меняется в «Серверы → Входы в РФ»."
                      value={settings.main_server_host} empty="не задан — в подписках нет входов через Hub" emptyWarning />
                    <ReadOnlySetting label="Адрес сайта (site_url)" hint="Ссылки в письмах и возврат после оплаты."
                      value={settings.site_url} empty="https://hiearth.ru (по умолчанию)" />
                  </>
                )}
              </Section>
            ))}
            <Section title="Уведомления">
              <p className="py-3 text-sm text-secondary">
                Тревоги в Telegram (бот и чат) настраиваются на странице <Link to="/alerts" className="text-accent hover:underline">«Алерты»</Link>,
                Telegram-прокси — на странице <Link to="/mtproto" className="text-accent hover:underline">«MTProto»</Link>.
              </p>
            </Section>
          </>
        )}
      </div>
    </div>
  )
}
