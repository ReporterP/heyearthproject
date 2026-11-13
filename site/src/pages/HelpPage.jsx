import { useState } from 'react'
import { Link } from 'react-router-dom'
import useSiteInfo from '../hooks/useSiteInfo.js'
import { useAuth } from '../auth/AuthContext.jsx'

const HAPP_IOS = 'https://apps.apple.com/us/app/happ-proxy-utility-plus/id6517741108'
const HAPP_ANDROID = 'https://play.google.com/store/apps/details?id=com.happproxy'
const HAPP_DESKTOP = 'https://www.happ.su/main/ru'

// supportHref turns the support_contact setting into a link: a Telegram @handle, a t.me / https
// link, an e-mail; anything else is shown as text.
export function supportHref(contact) {
  const c = String(contact || '').trim()
  if (!c) return ''
  if (/^@[A-Za-z0-9_]{4,}$/.test(c)) return `https://t.me/${c.slice(1)}`
  if (/^https:\/\//i.test(c)) return c
  if (/^t\.me\//i.test(c)) return `https://${c}`
  if (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(c)) return `mailto:${c}`
  return ''
}

const FAQ = [
  {
    q: 'Как подключиться',
    a: (loggedIn) => (
      <ol className="list-decimal pl-5 space-y-1.5">
        <li>Установите приложение Happ: <a className="text-brand hover:text-brand-light" href={HAPP_IOS} target="_blank" rel="noopener noreferrer">iPhone</a>, <a className="text-brand hover:text-brand-light" href={HAPP_ANDROID} target="_blank" rel="noopener noreferrer">Android</a>, <a className="text-brand hover:text-brand-light" href={HAPP_DESKTOP} target="_blank" rel="noopener noreferrer">Windows и macOS</a>.</li>
        <li>Откройте {loggedIn ? <Link className="text-brand hover:text-brand-light" to="/connect">«Подключение»</Link> : '«Подключение»'} в личном кабинете и нажмите «Подключить в приложении» — подписка добавится сама.</li>
        <li>Включите подключение переключателем в Happ.</li>
      </ol>
    ),
  },
  {
    q: 'Не работает на мобильном интернете',
    a: () => (
      <ul className="list-disc pl-5 space-y-1.5">
        <li>Обновите подписку в Happ (потяните список вниз или нажмите «Обновить»): адреса для мобильных сетей приходят с обновлением.</li>
        <li>Попробуйте другую строку подписки: если одна не соединяется, Happ позволяет выбрать соседнюю.</li>
        <li>Проверьте, что в Wi-Fi всё работает: если да, напишите нам название оператора и город — проверим вход для вашей сети.</li>
      </ul>
    ),
  },
  {
    q: 'Подписка закончилась или не продлилась',
    a: (loggedIn) => (
      <p>Подписка продлевается с баланса кошелька, если включено автопродление и денег хватает. Пополните кошелёк или продлите тариф на странице {loggedIn ? <Link className="text-brand hover:text-brand-light" to="/payment">«Тарифы»</Link> : '«Тарифы»'} — доступ вернётся сразу после оплаты.</p>
    ),
  },
  {
    q: 'Потерял ID аккаунта',
    a: () => (
      <p>ID — единственный ключ входа, поэтому восстановить его без пароля нельзя. Если вы задавали имя и пароль, войдите по имени. Если нет — напишите в поддержку, укажите дату и сумму оплаты: найдём аккаунт по платежу.</p>
    ),
  },
  {
    q: 'Как вернуть деньги',
    a: () => (
      <p>Условия и порядок — на странице <Link className="text-brand hover:text-brand-light" to="/refund">«Условия возврата»</Link>.</p>
    ),
  },
]

export default function HelpPage() {
  const { info, loaded } = useSiteInfo()
  const { isLoggedIn } = useAuth()
  const [open, setOpen] = useState(0)
  const contact = info.support_contact
  const contactLink = supportHref(contact)
  const email = info.support_email

  return (
    <section className="relative py-12 md:py-20">
      <div className="max-w-3xl mx-auto px-4 sm:px-6">
        <p className="eyebrow mb-2">Помощь</p>
        <h1 className="serif text-3xl md:text-4xl font-semibold mb-3">Если что-то не так</h1>
        <p className="text-text-secondary mb-10 max-w-xl">Ответы на частые вопросы. Не нашли свой — напишите нам, ответим в течение дня.</p>

        <div className="card-feat rounded-[22px] p-6 md:p-7 mb-8">
          <h2 className="serif text-lg font-semibold mb-3">Связаться с поддержкой</h2>
          {!loaded ? (
            <div className="skeleton h-5 w-48 rounded" />
          ) : contact || email ? (
            <div className="flex flex-col sm:flex-row gap-3">
              {contact && (contactLink
                ? <a href={contactLink} target="_blank" rel="noopener noreferrer" className="btn-primary !py-2.5 !text-sm">Написать: {contact}</a>
                : <p className="text-sm text-text-primary">{contact}</p>)}
              {email && <a href={`mailto:${email}`} className="btn-ghost !py-2.5 !text-sm">{email}</a>}
            </div>
          ) : (
            <p className="text-sm text-text-secondary">Контакты поддержки скоро появятся здесь. Пока опишите проблему через раздел «Документы» — там указаны реквизиты.</p>
          )}
          <p className="text-xs text-faint mt-3">Укажите свой ID (он в личном кабинете) — так мы быстрее найдём аккаунт.</p>
        </div>

        <div className="space-y-3">
          {FAQ.map((item, i) => (
            <div key={item.q} className="card rounded-[18px]">
              <button
                onClick={() => setOpen(open === i ? -1 : i)}
                aria-expanded={open === i}
                className="w-full flex items-center justify-between gap-3 px-5 py-4 text-left"
              >
                <span className="font-medium">{item.q}</span>
                <svg className={`w-4 h-4 text-brand transition-transform duration-300 ${open === i ? 'rotate-90' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.2}><path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" /></svg>
              </button>
              <div className="grid transition-all duration-300 ease-out" style={{ gridTemplateRows: open === i ? '1fr' : '0fr' }}>
                <div className="overflow-hidden">
                  <div className="px-5 pb-5 text-sm text-text-secondary leading-relaxed">{item.a(isLoggedIn)}</div>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
