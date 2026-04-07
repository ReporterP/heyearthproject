import { useState, useEffect } from 'react'

export default function RefundPage() {
  const [settings, setSettings] = useState({})
  useEffect(() => {
    fetch('/api/v1/legal-info').then(r => r.json()).then(setSettings).catch(() => {})
  }, [])

  const companyName = settings.legal_company_name || 'ИП Степанов Александр Владимирович'
  const email = settings.legal_email || 'alex_step04@mail.ru'

  return (
    <section className="py-10 md:py-16">
      <div className="max-w-3xl mx-auto px-4 sm:px-6">
        <h1 className="text-3xl font-bold mb-8">Условия возврата и отмены</h1>

        <div className="prose prose-invert prose-sm max-w-none space-y-6 text-text-secondary">
          <p className="text-sm">Дата последнего обновления: 07 апреля 2026 г.</p>

          <h2 className="text-lg font-semibold text-text-primary">1. Общие положения</h2>
          <p>Настоящие условия определяют порядок возврата денежных средств за услуги прокси-маршрутизации трафика, предоставляемые сервисом HiEarth ({companyName}).</p>

          <h2 className="text-lg font-semibold text-text-primary">2. Порядок предоставления услуги</h2>
          <p>Услуга прокси-маршрутизации предоставляется в электронном виде немедленно после успешной оплаты. Подписка активируется автоматически и действует в течение оплаченного периода.</p>

          <h2 className="text-lg font-semibold text-text-primary">3. Условия возврата</h2>
          <ul className="list-disc pl-5 space-y-2">
            <li><strong>В течение 3 дней</strong> с момента оплаты — полный возврат средств при условии, что объём использованного трафика не превысил 1 ГБ</li>
            <li><strong>При технической неисправности</strong> сервиса, длящейся более 24 часов — пропорциональный возврат за период недоступности</li>
            <li><strong>При ошибочном списании</strong> — полный возврат в течение 5 рабочих дней</li>
          </ul>

          <h2 className="text-lg font-semibold text-text-primary">4. Когда возврат не предоставляется</h2>
          <ul className="list-disc pl-5 space-y-2">
            <li>По истечении 3 дней с момента оплаты (при использовании более 1 ГБ)</li>
            <li>При нарушении пользователем условий использования сервиса</li>
            <li>При блокировке аккаунта за нарушение правил</li>
          </ul>

          <h2 className="text-lg font-semibold text-text-primary">5. Отмена подписки</h2>
          <p>Автопродление подписки можно отключить в любой момент в личном кабинете (раздел «Настройки»). При отключении автопродления текущая подписка продолжает действовать до конца оплаченного периода.</p>

          <h2 className="text-lg font-semibold text-text-primary">6. Как запросить возврат</h2>
          <p>Для оформления возврата свяжитесь с нами по электронной почте: <a href={`mailto:${email}`} className="text-brand hover:underline">{email}</a></p>
          <p>В обращении укажите:</p>
          <ul className="list-disc pl-5 space-y-1">
            <li>Ваш HeYearth ID</li>
            <li>Дату и сумму платежа</li>
            <li>Причину возврата</li>
          </ul>
          <p>Срок рассмотрения заявки — до 5 рабочих дней. Возврат осуществляется тем же способом, которым была произведена оплата.</p>

          <h2 className="text-lg font-semibold text-text-primary">7. Контакты</h2>
          <p>{companyName}</p>
          <p>Email: <a href={`mailto:${email}`} className="text-brand hover:underline">{email}</a></p>
        </div>
      </div>
    </section>
  )
}
