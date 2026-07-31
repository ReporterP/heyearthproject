import { useState, useEffect } from 'react'
import LegalToc from '../components/LegalToc.jsx'

export default function RefundPage() {
  const [settings, setSettings] = useState({})
  useEffect(() => {
    fetch('/api/v1/legal-info').then(r => r.json()).then(setSettings).catch(() => {})
  }, [])

  const companyName = settings.legal_company_name || 'ИП Степанов Александр Владимирович'
  const email = settings.legal_email || 'alex_step04@mail.ru'

  return (
    <section className="py-10 md:py-16">
      <div className="max-w-5xl mx-auto px-4 sm:px-6">
        <p className="eyebrow mb-4">Документы</p>
        <h1 className="serif text-3xl md:text-4xl font-semibold mb-2">Условия возврата и отмены</h1>
        <p className="text-sm text-faint mb-8">Дата последнего обновления: 07 апреля 2026 г.</p>

        <div className="grid lg:grid-cols-[16rem_1fr] gap-6 items-start">
        <div className="lg:order-2 min-w-0">
        <article id="legal-doc" className="card p-6 md:p-8 space-y-6 text-text-secondary text-[15px] leading-relaxed">
          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">1. Общие положения</h2>
            <p>Настоящие условия определяют порядок возврата денежных средств за услуги прокси-маршрутизации трафика, предоставляемые сервисом HiEarth ({companyName}).</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">2. Порядок предоставления услуги</h2>
            <p>Услуга прокси-маршрутизации предоставляется в электронном виде немедленно после успешной оплаты. Подписка активируется автоматически и действует в течение оплаченного периода.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">3. Условия возврата</h2>
            <ul className="list-disc pl-5 space-y-2">
              <li><strong className="text-text-primary">В течение 3 дней</strong> с момента оплаты — полный возврат средств при условии, что объём использованного трафика не превысил 1 ГБ</li>
              <li><strong className="text-text-primary">При технической неисправности</strong> сервиса, длящейся более 24 часов — пропорциональный возврат за период недоступности</li>
              <li><strong className="text-text-primary">При ошибочном списании</strong> — полный возврат в течение 5 рабочих дней</li>
            </ul>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">4. Когда возврат не предоставляется</h2>
            <ul className="list-disc pl-5 space-y-2">
              <li>По истечении 3 дней с момента оплаты (при использовании более 1 ГБ)</li>
              <li>При нарушении пользователем условий использования сервиса</li>
              <li>При ограничении аккаунта за нарушение правил сервиса</li>
            </ul>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">5. Отмена подписки</h2>
            <p>Автопродление подписки можно отключить в любой момент в личном кабинете (раздел «Настройки»). При отключении автопродления текущая подписка продолжает действовать до конца оплаченного периода.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">6. Как запросить возврат</h2>
            <p>Для оформления возврата свяжитесь с нами по электронной почте: <a href={`mailto:${email}`} className="text-brand hover:underline">{email}</a></p>
            <p className="mt-2">В обращении укажите:</p>
            <ul className="list-disc pl-5 space-y-1 mt-2">
              <li>Ваш HiEarth ID</li>
              <li>Дату и сумму платежа</li>
              <li>Причину возврата</li>
            </ul>
            <p className="mt-2">Срок рассмотрения заявки — до 5 рабочих дней. Возврат осуществляется тем же способом, которым была произведена оплата.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">7. Контакты</h2>
            <p>{companyName}</p>
            <p>Email: <a href={`mailto:${email}`} className="text-brand hover:underline">{email}</a></p>
          </div>
        </article>
        </div>
        <div className="lg:order-1"><LegalToc /></div>
        </div>
      </div>
    </section>
  )
}
