import LegalToc from '../components/LegalToc.jsx'
import useLegalInfo from '../hooks/useLegalInfo.js'

export default function PrivacyPage() {
  const s = useLegalInfo()
  const company = s.legal_company_name || '…'
  const email = s.legal_email || ''
  const address = s.legal_address || 'Указывается по запросу'

  return (
    <section className="py-10 md:py-16">
      <div className="max-w-5xl mx-auto px-4 sm:px-6">
        <p className="eyebrow mb-4">Документы</p>
        <h1 className="serif text-3xl md:text-4xl font-semibold mb-2">Политика конфиденциальности</h1>
        <p className="text-sm text-faint mb-8">Дата последнего обновления: 13 ноября 2026 г.</p>

        <div className="grid lg:grid-cols-[16rem_1fr] gap-6 items-start">
        <div className="lg:order-2 min-w-0">
        <article id="legal-doc" className="card p-6 md:p-8 space-y-6 text-text-secondary text-[15px] leading-relaxed">
          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">1. Общие положения</h2>
            <p>Настоящая Политика конфиденциальности (далее — Политика) определяет порядок обработки и защиты персональных данных пользователей сервиса HiEarth (hiearth.ru), предоставляемого {company} (далее — Оператор).</p>
            <p className="mt-2">Используя сервис, вы соглашаетесь с условиями настоящей Политики. Если вы не согласны с условиями, пожалуйста, не используйте сервис.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">2. Какие данные мы собираем</h2>
            <ul className="list-disc pl-5 space-y-1">
              <li>Идентификатор пользователя (HiEarth ID) — генерируется автоматически при регистрации</li>
              <li>Адрес электронной почты — если вы укажете его при оплате (для чека) или в личном кабинете (для чеков и напоминаний о подписке), а также при обращении в поддержку</li>
              <li>Платёжные данные — обрабатываются исключительно платёжным провайдером (ЮKassa), мы не храним данные банковских карт</li>
              <li>Техническая информация — платформа и язык при регистрации</li>
              <li>Данные об использовании трафика — объём загруженных/скачанных данных (без содержимого)</li>
            </ul>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">3. Цели обработки данных</h2>
            <ul className="list-disc pl-5 space-y-1">
              <li>Предоставление услуг прокси-маршрутизации трафика</li>
              <li>Обработка платежей и управление подписками</li>
              <li>Техническая поддержка пользователей</li>
              <li>Улучшение качества сервиса</li>
              <li>Выполнение требований законодательства РФ</li>
            </ul>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">4. Безопасность данных</h2>
            <p>Мы применяем следующие меры для защиты ваших данных:</p>
            <ul className="list-disc pl-5 space-y-1 mt-2">
              <li>Шифрование передачи данных (TLS 1.3) между клиентом и сервером</li>
              <li>Хеширование пользовательских паролей (bcrypt)</li>
              <li>Платёжные данные обрабатываются через сертифицированного провайдера (ЮKassa), соответствующего стандарту PCI DSS</li>
            </ul>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">5. Передача данных третьим лицам</h2>
            <p>Мы не продаём и не передаём персональные данные пользователей третьим лицам, за исключением:</p>
            <ul className="list-disc pl-5 space-y-1 mt-2">
              <li>Платёжный провайдер (ООО «ЮMoney», ЮKassa) — для обработки платежей</li>
              <li>По требованию уполномоченных государственных органов в соответствии с законодательством РФ</li>
            </ul>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">6. Хранение данных</h2>
            <p>Персональные данные хранятся на серверах, расположенных на территории Российской Федерации. Подробная статистика трафика хранится 45 дней, дальше — только суммарный объём за каждый день. Сессии входа удаляются по истечении срока (30 дней). Платёжные документы и чеки хранятся столько, сколько требует законодательство.</p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">7. Права пользователя</h2>
            <p>Вы имеете право:</p>
            <ul className="list-disc pl-5 space-y-1 mt-2">
              <li>Запросить информацию о хранимых данных</li>
              <li>Потребовать удаления своих данных</li>
              <li>Отозвать согласие на обработку данных</li>
              <li>Обратиться с жалобой в Роскомнадзор</li>
            </ul>
            <p className="mt-2">Для реализации своих прав обратитесь по адресу: <a href={`mailto:${email}`} className="text-brand hover:underline">{email}</a></p>
          </div>

          <div>
            <h2 className="serif text-lg font-semibold text-text-primary mb-2">8. Контактная информация</h2>
            <p>{company}</p>
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
