import { useState, useEffect } from 'react'
import LegalToc from '../components/LegalToc.jsx'

export default function OfferPage() {
  const [s, setS] = useState({})
  useEffect(() => { fetch('/api/v1/legal-info').then(r => r.json()).then(setS).catch(() => {}) }, [])
  const company = s.legal_company_name || 'ИП Степанов Александр Владимирович'
  const email = s.legal_email || 'alex_step04@mail.ru'
  const address = s.legal_address || 'Указывается по запросу'
  const inn = s.legal_inn || ''

  return (
    <section className="py-10 md:py-16">
      <div className="max-w-5xl mx-auto px-4 sm:px-6">
        <p className="eyebrow mb-4">Документы</p>
        <h1 className="serif text-3xl md:text-4xl font-semibold mb-2">Договор-оферта</h1>
        <p className="text-sm text-faint mb-8">Дата последнего обновления: 07 апреля 2026 г.</p>

        <div className="grid lg:grid-cols-[16rem_1fr] gap-6 items-start">
        <div className="lg:order-2 min-w-0">
        <article id="legal-doc" className="card p-6 md:p-8 space-y-6 text-text-secondary text-[15px] leading-relaxed">
          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">1. Предмет договора</h2>
            <p>{company} (далее — Исполнитель) предоставляет услуги прокси-маршрутизации интернет-трафика (далее — Сервис) посредством веб-сайта hiearth.ru и приложения HiEarth.</p>
            <p className="mt-2">Настоящий документ является публичной офертой в соответствии со ст. 435 и ст. 437 ГК РФ. Оплата услуг является акцептом настоящей оферты.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">2. Описание услуги</h2>
            <p>Сервис предоставляет услуги маршрутизации интернет-трафика через собственную инфраструктуру серверов. Услуга предоставляется на территории Российской Федерации в электронном виде немедленно после оплаты.</p>
            <ul className="list-disc pl-5 space-y-1 mt-2">
              <li>Тарифные планы и их стоимость указаны на странице оплаты (hiearth.ru/payment)</li>
              <li>Срок действия подписки указан в описании выбранного тарифа</li>
              <li>Лимиты трафика (при наличии) указаны в описании тарифа</li>
            </ul>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">3. Порядок оплаты</h2>
            <p>Оплата производится через платёжную систему ЮKassa (ООО «ЮMoney»). Исполнитель не обрабатывает и не хранит данные банковских карт.</p>
            <p className="mt-2">Поддерживаемые способы оплаты: банковская карта (Visa, MasterCard, МИР), Система быстрых платежей (СБП), ЮMoney, T-Pay, Сбербанк Онлайн, Альфа-Клик и другие методы, доступные в ЮKassa.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">4. Автопродление</h2>
            <p>При оформлении подписки автопродление включается по умолчанию. За 1 день до окончания подписки с сохранённого способа оплаты списывается стоимость следующего периода. Автопродление можно отключить в личном кабинете.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">5. Возврат средств</h2>
            <p>Условия возврата описаны на странице <a href="/refund" className="text-brand hover:underline">«Условия возврата и отмены»</a>.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">6. Ограничения использования</h2>
            <p>Запрещается использование Сервиса для:</p>
            <ul className="list-disc pl-5 space-y-1 mt-2">
              <li>Действий, нарушающих законодательство РФ</li>
              <li>Распространения вредоносного ПО</li>
              <li>Рассылки спама</li>
              <li>DDoS-атак и иных деструктивных действий</li>
            </ul>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">7. Реквизиты Исполнителя</h2>
            <p>{company}</p>
            {inn && <p>ИНН: {inn}</p>}
            {address && <p>Адрес: {address}</p>}
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
