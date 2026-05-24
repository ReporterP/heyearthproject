import { useState, useEffect } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { getSettings, updateSetting } from '../api/admin.js'
import SettingCard from '../components/SettingCard.jsx'
import {
  Button,
  Card,
  CardHeader,
  CardBody,
  PageHeader,
  Skeleton,
} from '../components/ui/index.js'

export default function SettingsPage() {
  const { t, locale, setLocale } = useT()
  const { admin, logout, can } = useAuth()
  const toast = useToast()

  const [settings, setSettings] = useState({})
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (can('settings.read')) {
      getSettings()
        .then(data => setSettings(data.settings || data || {}))
        .catch(err => toast.error(err.message))
        .finally(() => setLoading(false))
    } else {
      setLoading(false)
    }
  }, [])

  async function saveSetting(key, value) {
    try {
      await updateSetting(key, value)
      setSettings(prev => ({ ...prev, [key]: value }))
      toast.success(t.save + ' OK')
    } catch (err) {
      toast.error(err.message)
    }
  }

  function changeTheme(mode) {
    localStorage.setItem('admin_theme_mode', mode)
    document.documentElement.classList.toggle('light', mode === 'light')
  }

  const currentTheme = localStorage.getItem('admin_theme_mode') || 'dark'

  return (
    <div className="max-w-2xl">
      <PageHeader title={t.settingsTitle} subtitle="Управление аккаунтом и настройками платформы" />

      <div className="space-y-6">
        {/* Account */}
        <Card>
          <CardHeader><h3 className="text-lg font-semibold text-primary">{t.settingsAccount}</h3></CardHeader>
          <CardBody>
            <p className="text-sm text-secondary mb-1">{admin?.username}</p>
            <p className="text-sm text-secondary mb-4">{admin?.display_name}</p>
            <Button variant="danger" size="md" onClick={logout}>{t.settingsLogout}</Button>
          </CardBody>
        </Card>

        {/* Theme & Language */}
        <Card>
          <CardHeader><h3 className="text-lg font-semibold text-primary">{t.settingsTheme}</h3></CardHeader>
          <CardBody>
            <div className="flex gap-2 mb-4">
              {['dark', 'light'].map(mode => (
                <Button
                  key={mode}
                  variant={currentTheme === mode ? 'primary' : 'secondary'}
                  size="md"
                  onClick={() => changeTheme(mode)}
                >
                  {mode === 'dark' ? t.themeDark : t.themeLight}
                </Button>
              ))}
            </div>
            <h3 className="text-lg font-semibold mb-3 text-primary">{t.settingsLanguage}</h3>
            <div className="flex gap-2">
              {['ru', 'en'].map(l => (
                <Button
                  key={l}
                  variant={locale === l ? 'primary' : 'secondary'}
                  size="md"
                  onClick={() => setLocale(l)}
                >
                  {l.toUpperCase()}
                </Button>
              ))}
            </div>
          </CardBody>
        </Card>

        {/* Server settings */}
        {can('settings.read') && (
          <>
            <Card>
              <CardHeader><h3 className="text-lg font-semibold text-primary">{t.settingsServerSection}</h3></CardHeader>
              <CardBody>
                {loading ? <Skeleton className="h-24" /> : (
                  <div className="divide-y divide-default">
                    <SettingCard label="ЮKassa Shop ID" hint="shopId — идентификатор магазина в личном кабинете ЮKassa" value={settings.yookassa_shop_id} onSave={v => saveSetting('yookassa_shop_id', v)} />
                    <SettingCard label="ЮKassa Secret Key" hint="Секретный ключ API. Никогда не публикуйте его. ЛК ЮKassa → Интеграция → API ключ" value={settings.yookassa_secret_key} onSave={v => saveSetting('yookassa_secret_key', v)} type="password" />
                    <SettingCard label="Тестовый режим" hint="true — использовать тестовый магазин ЮKassa (реальные деньги не списываются). В ЛК тестового магазина настройте Webhook URL: https://hiearth.ru/api/v1/webhooks/yookassa" value={settings.yookassa_test_mode || 'true'} onSave={v => saveSetting('yookassa_test_mode', v)} type="toggle" />
                    <SettingCard label="Email для чеков (fallback)" hint="Если пользователь не указал email при оплате, чек придёт на этот адрес. Фискализация по 54-ФЗ обязательна." value={settings.receipt_fallback_email} onSave={v => saveSetting('receipt_fallback_email', v)} />
                  </div>
                )}
              </CardBody>
            </Card>

            <Card>
              <CardHeader><h3 className="text-lg font-semibold text-primary">SMTP (email уведомления)</h3></CardHeader>
              <CardBody>
                {loading ? <Skeleton className="h-24" /> : (
                  <div className="divide-y divide-default">
                    <SettingCard label="Отправка включена" hint="true/false — если false, email не отправляется (но логируется в email_log)" value={settings.smtp_enabled || 'false'} onSave={v => saveSetting('smtp_enabled', v)} type="toggle" />
                    <SettingCard label="SMTP Host" hint="Например smtp.yandex.ru или smtp.mail.ru" value={settings.smtp_host} onSave={v => saveSetting('smtp_host', v)} />
                    <SettingCard label="SMTP Port" hint="587 (STARTTLS) или 465 (implicit TLS)" value={settings.smtp_port || '587'} onSave={v => saveSetting('smtp_port', v)} />
                    <SettingCard label="SMTP User" hint="Обычно email-ящик полностью" value={settings.smtp_user} onSave={v => saveSetting('smtp_user', v)} />
                    <SettingCard label="SMTP Password" hint="Пароль приложения (app password). Для Gmail/Yandex — создать в настройках почты" value={settings.smtp_password} onSave={v => saveSetting('smtp_password', v)} type="password" />
                    <SettingCard label="From email" hint="Email отправителя (должен совпадать с SMTP User или быть в разрешённых)" value={settings.smtp_from_email} onSave={v => saveSetting('smtp_from_email', v)} />
                    <SettingCard label="From name" hint="Имя отправителя, например HiEarth" value={settings.smtp_from_name || 'HiEarth'} onSave={v => saveSetting('smtp_from_name', v)} />
                    <SettingCard label="Use TLS" hint="true для шифрования (рекомендуется)" value={settings.smtp_use_tls || 'true'} onSave={v => saveSetting('smtp_use_tls', v)} type="toggle" />
                  </div>
                )}
              </CardBody>
            </Card>

            <Card>
              <CardHeader><h3 className="text-lg font-semibold text-primary">Юридические данные</h3></CardHeader>
              <CardBody>
                {loading ? <Skeleton className="h-24" /> : (
                  <div className="divide-y divide-default">
                    <SettingCard label="Название организации" hint="Полное наименование ИП или ООО" value={settings.legal_company_name} onSave={v => saveSetting('legal_company_name', v)} />
                    <SettingCard label="ИНН" hint="ИНН индивидуального предпринимателя или организации" value={settings.legal_inn} onSave={v => saveSetting('legal_inn', v)} />
                    <SettingCard label="Email" hint="Контактный email для связи" value={settings.legal_email} onSave={v => saveSetting('legal_email', v)} />
                    <SettingCard label="Телефон" hint="Контактный телефон" value={settings.legal_phone} onSave={v => saveSetting('legal_phone', v)} />
                    <SettingCard label="Юридический адрес" hint="Адрес регистрации ИП/ООО" value={settings.legal_address} onSave={v => saveSetting('legal_address', v)} />
                  </div>
                )}
              </CardBody>
            </Card>

            <Card>
              <CardHeader><h3 className="text-lg font-semibold text-primary">Блок «Поддержать разработчика»</h3></CardHeader>
              <CardBody>
                {loading ? <Skeleton className="h-24" /> : (
                  <div className="divide-y divide-default">
                    <SettingCard label="Включить блок донатов" hint="Показывать блок «Поддержать разработчика» в личном кабинете пользователей" value={settings.donation_enabled || 'true'} onSave={v => saveSetting('donation_enabled', v)} type="toggle" />
                    <SettingCard label="Заголовок блока" hint="Заголовок для блока донатов" value={settings.donation_title} onSave={v => saveSetting('donation_title', v)} />
                    <SettingCard label="Текст блока" hint="Основной текст блока донатов (поддерживает простой текст)" value={settings.donation_text} onSave={v => saveSetting('donation_text', v)} />
                  </div>
                )}
              </CardBody>
            </Card>

            <Card>
              <CardHeader><h3 className="text-lg font-semibold text-primary">{t.settingsHubServerSection}</h3></CardHeader>
              <CardBody>
                {loading ? <Skeleton className="h-24" /> : (
                  <div className="divide-y divide-default">
                    <SettingCard label={t.settingsHubMainServerHost} hint={t.settingsHubMainServerHostHint} value={settings.hub_main_server_host} onSave={v => saveSetting('hub_main_server_host', v)} />
                    <SettingCard label={t.settingsHubVlessPort} hint={t.settingsHubVlessPortHint} value={settings.hub_vless_port} onSave={v => saveSetting('hub_vless_port', v)} />
                    <SettingCard label={t.settingsHubSocks5Port} hint={t.settingsHubSocks5PortHint} value={settings.hub_socks5_port} onSave={v => saveSetting('hub_socks5_port', v)} />
                    <SettingCard label={t.settingsHubHttpProxyPort} hint={t.settingsHubHttpProxyPortHint} value={settings.hub_http_proxy_port} onSave={v => saveSetting('hub_http_proxy_port', v)} />
                    <SettingCard label={t.settingsApiEndpoints} hint={t.settingsApiEndpointsHint} value={settings.api_endpoints} onSave={v => saveSetting('api_endpoints', v)} />
                  </div>
                )}
              </CardBody>
            </Card>

            <Card>
              <CardHeader><h3 className="text-lg font-semibold text-primary">{t.settingsB2bTariffsSection}</h3></CardHeader>
              <CardBody>
                {loading ? <Skeleton className="h-24" /> : (
                  <div className="divide-y divide-default">
                    <SettingCard label={t.settingsB2bPricePerUser} hint={t.settingsB2bPricePerUserHint} value={settings.b2b_price_per_user} onSave={v => saveSetting('b2b_price_per_user', v)} />
                    <SettingCard label={t.settingsB2bPriceOurServers} hint={t.settingsB2bPriceOurServersHint} value={settings.b2b_price_our_servers} onSave={v => saveSetting('b2b_price_our_servers', v)} />
                    <SettingCard label={t.settingsB2bBaseOwnServers} hint={t.settingsB2bBaseOwnServersHint} value={settings.b2b_base_own_servers} onSave={v => saveSetting('b2b_base_own_servers', v)} />
                    <SettingCard label={t.settingsB2bPriceExtraServer} hint={t.settingsB2bPriceExtraServerHint} value={settings.b2b_price_extra_server} onSave={v => saveSetting('b2b_price_extra_server', v)} />
                    <SettingCard label={t.settingsB2bPricePremiumUser} hint={t.settingsB2bPricePremiumUserHint} value={settings.b2b_price_premium_user} onSave={v => saveSetting('b2b_price_premium_user', v)} />
                    <SettingCard label={t.settingsB2bPriceUnlimited} hint={t.settingsB2bPriceUnlimitedHint} value={settings.b2b_price_unlimited} onSave={v => saveSetting('b2b_price_unlimited', v)} />
                  </div>
                )}
              </CardBody>
            </Card>

            <Card>
              <CardHeader><h3 className="text-lg font-semibold text-primary">{t.settingsAppBuildSection}</h3></CardHeader>
              <CardBody>
                {loading ? <Skeleton className="h-24" /> : (
                  <div className="divide-y divide-default">
                    <SettingCard label={t.settingsAppBuildRepoUrl} value={settings.app_build_repo_url} onSave={v => saveSetting('app_build_repo_url', v)} />
                    <SettingCard label={t.settingsAppBuildRepoBranch} value={settings.app_build_repo_branch} onSave={v => saveSetting('app_build_repo_branch', v)} />
                    <SettingCard label={t.settingsAppBuildSiteUrl} value={settings.app_build_site_url} onSave={v => saveSetting('app_build_site_url', v)} />
                    <SettingCard label={t.settingsAppBuildApiBase} value={settings.app_build_api_base} onSave={v => saveSetting('app_build_api_base', v)} />
                    <SettingCard label={t.settingsAppBuildPlatforms} value={settings.app_build_platforms} onSave={v => saveSetting('app_build_platforms', v)} />
                  </div>
                )}
              </CardBody>
            </Card>

            {/* Auto-Tuner moved to separate page /auto-tuner */}
          </>
        )}
      </div>
    </div>
  )
}
