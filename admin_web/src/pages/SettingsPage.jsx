import { useState, useEffect } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { getSettings, updateSetting } from '../api/admin.js'
import SettingCard from '../components/SettingCard.jsx'

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

  return (
    <div className="max-w-2xl space-y-8">
      <h2 className="text-2xl font-bold">{t.settingsTitle}</h2>

      {/* Account */}
      <section className="bg-surface-card rounded-xl border border-white/10 p-5">
        <h3 className="text-lg font-semibold mb-3">{t.settingsAccount}</h3>
        <p className="text-sm text-text-secondary mb-1">{admin?.username}</p>
        <p className="text-sm text-text-secondary mb-4">{admin?.display_name}</p>
        <button onClick={logout} className="px-4 py-2 rounded-lg bg-red-600 text-white text-sm hover:bg-red-700 transition">
          {t.settingsLogout}
        </button>
      </section>

      {/* Theme & Language */}
      <section className="bg-surface-card rounded-xl border border-white/10 p-5">
        <h3 className="text-lg font-semibold mb-3">{t.settingsTheme}</h3>
        <div className="flex gap-2 mb-4">
          {['dark', 'light'].map(mode => (
            <button key={mode} onClick={() => changeTheme(mode)}
              className={`px-4 py-2 rounded-lg text-sm transition ${
                (localStorage.getItem('admin_theme_mode') || 'dark') === mode
                  ? 'bg-brand text-white' : 'bg-surface-alt text-text-secondary hover:text-text-primary'
              }`}
            >
              {mode === 'dark' ? t.themeDark : t.themeLight}
            </button>
          ))}
        </div>
        <h3 className="text-lg font-semibold mb-3">{t.settingsLanguage}</h3>
        <div className="flex gap-2">
          {['ru', 'en'].map(l => (
            <button key={l} onClick={() => setLocale(l)}
              className={`px-4 py-2 rounded-lg text-sm transition ${
                locale === l ? 'bg-brand text-white' : 'bg-surface-alt text-text-secondary hover:text-text-primary'
              }`}
            >
              {l.toUpperCase()}
            </button>
          ))}
        </div>
      </section>

      {/* Server settings */}
      {can('settings.read') && (
        <>
          <section className="bg-surface-card rounded-xl border border-white/10 p-5">
            <h3 className="text-lg font-semibold mb-3">{t.settingsServerSection}</h3>
            {loading ? <p className="text-text-secondary">{t.loading}</p> : (
              <div className="divide-y divide-white/5">
                <SettingCard label="Robokassa Login" hint="MerchantLogin — идентификатор магазина в Robokassa" value={settings.robokassa_login} onSave={v => saveSetting('robokassa_login', v)} />
                <SettingCard label="Robokassa Password #1" hint="Пароль для подписи платежей (используется при создании ссылки оплаты)" value={settings.robokassa_password1} onSave={v => saveSetting('robokassa_password1', v)} type="password" />
                <SettingCard label="Robokassa Password #2" hint="Пароль для проверки webhook (ResultURL). Настройте ResultURL в Robokassa: https://hiearth.ru/api/webhooks/robokassa" value={settings.robokassa_password2} onSave={v => saveSetting('robokassa_password2', v)} type="password" />
                <SettingCard label="Тестовый режим" hint="Включить тестовый режим Robokassa (IsTest=1)" value={settings.robokassa_test_mode || 'true'} onSave={v => saveSetting('robokassa_test_mode', v)} type="toggle" />
                <SettingCard label="Robokassa Partner ID" hint="(Опционально) Для автоматических возвратов через API. Без него возвраты через панель Robokassa" value={settings.robokassa_partner_id} onSave={v => saveSetting('robokassa_partner_id', v)} />
                <SettingCard label="Email для чеков (fallback)" hint="Если пользователь не указал email при оплате, чек придёт на этот адрес" value={settings.receipt_fallback_email} onSave={v => saveSetting('receipt_fallback_email', v)} />
              </div>
            )}
          </section>

          <section className="bg-surface-card rounded-xl border border-white/10 p-5">
            <h3 className="text-lg font-semibold mb-3">Юридические данные</h3>
            {!loading && (
              <div className="divide-y divide-white/5">
                <SettingCard label="Название организации" hint="Полное наименование ИП или ООО" value={settings.legal_company_name} onSave={v => saveSetting('legal_company_name', v)} />
                <SettingCard label="ИНН" hint="ИНН индивидуального предпринимателя или организации" value={settings.legal_inn} onSave={v => saveSetting('legal_inn', v)} />
                <SettingCard label="Email" hint="Контактный email для связи" value={settings.legal_email} onSave={v => saveSetting('legal_email', v)} />
                <SettingCard label="Телефон" hint="Контактный телефон" value={settings.legal_phone} onSave={v => saveSetting('legal_phone', v)} />
                <SettingCard label="Юридический адрес" hint="Адрес регистрации ИП/ООО" value={settings.legal_address} onSave={v => saveSetting('legal_address', v)} />
              </div>
            )}
          </section>

          <section className="bg-surface-card rounded-xl border border-white/10 p-5">
            <h3 className="text-lg font-semibold mb-3">Блок «Поддержать разработчика»</h3>
            {!loading && (
              <div className="divide-y divide-white/5">
                <SettingCard label="Включить блок донатов" hint="Показывать блок «Поддержать разработчика» в личном кабинете пользователей" value={settings.donation_enabled || 'true'} onSave={v => saveSetting('donation_enabled', v)} type="toggle" />
                <SettingCard label="Заголовок блока" hint="Заголовок для блока донатов" value={settings.donation_title} onSave={v => saveSetting('donation_title', v)} />
                <SettingCard label="Текст блока" hint="Основной текст блока донатов (поддерживает простой текст)" value={settings.donation_text} onSave={v => saveSetting('donation_text', v)} />
              </div>
            )}
          </section>

          <section className="bg-surface-card rounded-xl border border-white/10 p-5">
            <h3 className="text-lg font-semibold mb-3">{t.settingsHubServerSection}</h3>
            {!loading && (
              <div className="divide-y divide-white/5">
                <SettingCard label={t.settingsHubMainServerHost} hint={t.settingsHubMainServerHostHint} value={settings.hub_main_server_host} onSave={v => saveSetting('hub_main_server_host', v)} />
                <SettingCard label={t.settingsHubVlessPort} hint={t.settingsHubVlessPortHint} value={settings.hub_vless_port} onSave={v => saveSetting('hub_vless_port', v)} />
                <SettingCard label={t.settingsHubSocks5Port} hint={t.settingsHubSocks5PortHint} value={settings.hub_socks5_port} onSave={v => saveSetting('hub_socks5_port', v)} />
                <SettingCard label={t.settingsHubHttpProxyPort} hint={t.settingsHubHttpProxyPortHint} value={settings.hub_http_proxy_port} onSave={v => saveSetting('hub_http_proxy_port', v)} />
                <SettingCard label={t.settingsApiEndpoints} hint={t.settingsApiEndpointsHint} value={settings.api_endpoints} onSave={v => saveSetting('api_endpoints', v)} />
              </div>
            )}
          </section>

          <section className="bg-surface-card rounded-xl border border-white/10 p-5">
            <h3 className="text-lg font-semibold mb-3">{t.settingsB2bTariffsSection}</h3>
            {!loading && (
              <div className="divide-y divide-white/5">
                <SettingCard label={t.settingsB2bPricePerUser} hint={t.settingsB2bPricePerUserHint} value={settings.b2b_price_per_user} onSave={v => saveSetting('b2b_price_per_user', v)} />
                <SettingCard label={t.settingsB2bPriceOurServers} hint={t.settingsB2bPriceOurServersHint} value={settings.b2b_price_our_servers} onSave={v => saveSetting('b2b_price_our_servers', v)} />
                <SettingCard label={t.settingsB2bBaseOwnServers} hint={t.settingsB2bBaseOwnServersHint} value={settings.b2b_base_own_servers} onSave={v => saveSetting('b2b_base_own_servers', v)} />
                <SettingCard label={t.settingsB2bPriceExtraServer} hint={t.settingsB2bPriceExtraServerHint} value={settings.b2b_price_extra_server} onSave={v => saveSetting('b2b_price_extra_server', v)} />
                <SettingCard label={t.settingsB2bPricePremiumUser} hint={t.settingsB2bPricePremiumUserHint} value={settings.b2b_price_premium_user} onSave={v => saveSetting('b2b_price_premium_user', v)} />
                <SettingCard label={t.settingsB2bPriceUnlimited} hint={t.settingsB2bPriceUnlimitedHint} value={settings.b2b_price_unlimited} onSave={v => saveSetting('b2b_price_unlimited', v)} />
              </div>
            )}
          </section>

          <section className="bg-surface-card rounded-xl border border-white/10 p-5">
            <h3 className="text-lg font-semibold mb-3">{t.settingsAppBuildSection}</h3>
            {!loading && (
              <div className="divide-y divide-white/5">
                <SettingCard label={t.settingsAppBuildRepoUrl} value={settings.app_build_repo_url} onSave={v => saveSetting('app_build_repo_url', v)} />
                <SettingCard label={t.settingsAppBuildRepoBranch} value={settings.app_build_repo_branch} onSave={v => saveSetting('app_build_repo_branch', v)} />
                <SettingCard label={t.settingsAppBuildSiteUrl} value={settings.app_build_site_url} onSave={v => saveSetting('app_build_site_url', v)} />
                <SettingCard label={t.settingsAppBuildApiBase} value={settings.app_build_api_base} onSave={v => saveSetting('app_build_api_base', v)} />
                <SettingCard label={t.settingsAppBuildPlatforms} value={settings.app_build_platforms} onSave={v => saveSetting('app_build_platforms', v)} />
              </div>
            )}
          </section>

          {/* Auto-Tuner moved to separate page /auto-tuner */}
        </>
      )}
    </div>
  )
}

