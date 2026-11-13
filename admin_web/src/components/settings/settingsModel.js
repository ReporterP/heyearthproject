// «Настройки» as pure rules (node --test): the blocks, each field's kind and the check the API
// repeats (internal/admin/settings_rules.go editableSettings). Only these keys are written here.

export const SETTINGS_SECTIONS = [
  {
    key: 'payments', title: 'Платежи',
    fields: [
      { key: 'yookassa_shop_id', label: 'ЮKassa: Shop ID', hint: 'Идентификатор магазина в личном кабинете ЮKassa', kind: 'text' },
      { key: 'yookassa_secret_key', label: 'ЮKassa: секретный ключ', kind: 'secret' },
      { key: 'yookassa_test_mode', label: 'Тестовый магазин ЮKassa', hint: 'Включено — реальные деньги не списываются', kind: 'bool', def: 'true' },
      { key: 'receipt_fallback_email', label: 'Email для чеков, если клиент не указал свой', hint: 'Чек по 54-ФЗ обязателен: без email клиента он уходит сюда', kind: 'email' },
      { key: 'wallet_min_topup_rub', label: 'Минимальное пополнение кошелька, ₽', kind: 'int', min: 1, max: 100000, def: '100' },
      { key: 'early_renewal_days_threshold', label: 'За сколько дней до конца предлагать продлить', hint: '0 — не предлагать', kind: 'int', min: 0, max: 30, def: '3' },
    ],
  },
  {
    key: 'mail', title: 'Почта',
    fields: [
      { key: 'smtp_enabled', label: 'Отправлять письма', hint: 'Выключено — письма только записываются в журнал «Письма»', kind: 'bool', def: 'false' },
      { key: 'smtp_host', label: 'SMTP-сервер', hint: 'Например smtp.yandex.ru', kind: 'host' },
      { key: 'smtp_port', label: 'Порт', hint: '587 (STARTTLS) или 465 (TLS)', kind: 'int', min: 1, max: 65535, def: '587' },
      { key: 'smtp_user', label: 'Логин', kind: 'text' },
      { key: 'smtp_password', label: 'Пароль', kind: 'secret' },
      { key: 'smtp_from_email', label: 'Адрес отправителя', kind: 'email' },
      { key: 'smtp_from_name', label: 'Имя отправителя', kind: 'text', def: 'HiEarth' },
      { key: 'smtp_use_tls', label: 'Шифрование (TLS)', kind: 'bool', def: 'true' },
    ],
  },
  {
    key: 'legal', title: 'Юр. данные и поддержка',
    fields: [
      { key: 'legal_company_name', label: 'Название (ИП или ООО)', kind: 'text' },
      { key: 'legal_inn', label: 'ИНН', hint: '10 или 12 цифр', kind: 'inn' },
      { key: 'legal_email', label: 'Email для связи', kind: 'email' },
      { key: 'legal_phone', label: 'Телефон', kind: 'text' },
      { key: 'legal_address', label: 'Юридический адрес', kind: 'text' },
      { key: 'support_contact', label: 'Контакт поддержки на сайте', hint: 'Например @hiearth_support или support@…', kind: 'text' },
    ],
  },
  {
    key: 'limits', title: 'Лимиты',
    fields: [
      { key: 'quota_cutoff_enabled', label: 'Отключать при исчерпании трафика тарифа', hint: 'На всех протоколах. Выключено — лимит тарифа только показывается', kind: 'bool', def: 'false' },
    ],
  },
  {
    key: 'referral', title: 'Приведи друга',
    fields: [
      { key: 'referral_enabled', label: 'Программа включена', kind: 'bool', def: 'false' },
      { key: 'referral_reward_referrer_rub', label: 'Бонус пригласившему, ₽', kind: 'int', min: 0, max: 10000, def: '100' },
      { key: 'referral_reward_friend_rub', label: 'Бонус другу, ₽', kind: 'int', min: 0, max: 10000, def: '100' },
      { key: 'referral_min_purchase_rub', label: 'Минимальная первая покупка друга, ₽', hint: 'Бонус — после первой покупки не меньше этой суммы реальными деньгами', kind: 'int', min: 0, max: 100000, def: '250' },
      { key: 'referral_monthly_cap', label: 'Бонусов одному пригласившему в месяц, не больше', kind: 'int', min: 0, max: 1000, def: '10' },
    ],
  },
  {
    key: 'connection', title: 'Подключение',
    fields: [
      { key: 'hub_main_server_host', label: 'Адрес Hub для приложений', hint: 'Домен или IP, без http:// и порта', kind: 'host' },
      { key: 'api_endpoints', label: 'Адреса API для приложения', hint: 'Через запятую, по порядку попыток', kind: 'text' },
      { key: 'b2b_enabled', label: 'Раздел B2B (компании)', hint: 'Выключено — разделы «Компании» и «B2B-аналитика» скрыты, публичные страницы бизнеса отвечают 404', kind: 'bool', def: 'false' },
    ],
  },
]

export const SETTING_KEYS = SETTINGS_SECTIONS.flatMap((s) => s.fields.map((f) => f.key))

const HOST_RE = /^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$/
const IPV4_RE = /^(\d{1,3}\.){3}\d{1,3}$/
const EMAIL_RE = /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/

// checkSetting: the value to send or the error in words (the API checks the same).
export function checkSetting(field, raw) {
  const v = String(raw ?? '').trim()
  switch (field.kind) {
    case 'bool':
      return v === 'true' || v === 'false' ? { value: v, error: '' } : { value: null, error: 'Значение: включено или выключено' }
    case 'int': {
      if (!/^-?\d+$/.test(v)) return { value: null, error: `Целое число от ${field.min} до ${field.max}` }
      const n = Number(v)
      return n >= field.min && n <= field.max ? { value: String(n), error: '' } : { value: null, error: `Целое число от ${field.min} до ${field.max}` }
    }
    case 'email':
      return v === '' || EMAIL_RE.test(v) ? { value: v, error: '' } : { value: null, error: 'Неверный email' }
    case 'host':
      return v === '' || HOST_RE.test(v) || IPV4_RE.test(v) ? { value: v, error: '' } : { value: null, error: 'Имя хоста или IP, без http:// и порта' }
    case 'inn':
      return v === '' || /^(\d{10}|\d{12})$/.test(v) ? { value: v, error: '' } : { value: null, error: 'ИНН — 10 или 12 цифр' }
    default:
      return { value: v, error: '' }
  }
}

// shownValue: what a field shows — the stored value or the API's default.
export const shownValue = (field, settings) => {
  const v = settings?.[field.key]
  return v == null || v === '' ? (field.def ?? '') : String(v)
}
