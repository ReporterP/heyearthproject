import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import SiteHeader from '../components/SiteHeader.jsx'
import SiteFooter from '../components/SiteFooter.jsx'
import Button from '../components/Button.jsx'
import Alert from '../components/Alert.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import Skeleton from '../components/Skeleton.jsx'
import Toc, { useScrollSpy } from '../components/Toc.jsx'
import useSiteInfo from '../hooks/useSiteInfo.js'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { HAPP, supportHref } from '../lib/apps.js'

// Помощь (spec 06 §6.2, artboard «Помощь»). Read. One primary button — «Выбрать тариф».
const TOPICS = [
  { id: 'connect', label: 'Как подключиться' },
  { id: 'mobile', label: 'Не работает на мобильном' },
  { id: 'expired', label: 'Подписка закончилась' },
  { id: 'lost-id', label: 'Потерял ID или пароль' },
  { id: 'refund', label: 'Как вернуть деньги' },
  { id: 'support', label: 'Связаться с поддержкой' },
]

export default function HelpPage() {
  const { isLoggedIn } = useAuth()
  const { info, loaded } = useSiteInfo()
  const narrow = useMedia('(max-width: 1024px)')
  const [current] = useScrollSpy(TOPICS.map((t) => t.id))
  usePageMeta('Помощь — HiEarth', { description: 'Как подключиться, что делать, если не работает на мобильном интернете, как продлить подписку и вернуть деньги.' })
  const guard = (to) => (isLoggedIn ? to : `/login?redirect=${encodeURIComponent(to)}`)
  const contact = info.support_contact
  const contactLink = supportHref(contact)
  const email = info.support_email

  return (
    <>
      <SiteHeader />
      <main id="main" className="container" style={{ paddingTop: narrow ? 32 : 56, paddingBottom: 96 }}>
        <h1 className="t-d2">Помощь</h1>
        <p className="t-lead" style={{ marginTop: 12, maxWidth: 640 }}>Ответы на частые вопросы. Не нашли свой — напишите нам, ответим в течение дня.</p>

        <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : '240px minmax(0, 1fr)', gap: narrow ? 16 : 48, marginTop: narrow ? 24 : 48, alignItems: 'start' }}>
          <Toc
            items={TOPICS}
            current={current}
            label="Темы"
            style={narrow
              ? { position: 'sticky', top: 'var(--header-h)', zIndex: 5, flexDirection: 'row', overflowX: 'auto', background: 'var(--bg)', padding: '8px 0', margin: '0 -16px', paddingInline: 16, whiteSpace: 'nowrap', scrollbarWidth: 'none' }
              : { position: 'sticky', top: 92 }}
          />
          <div className="stack gap-4">
            <section id="connect" className="card" style={{ padding: narrow ? 22 : 30 }}>
              <h2 className="t-h2">Как подключиться</h2>
              <ol className="num">
                <li><span><b>Установите приложение Happ.</b> Оно бесплатное.</span></li>
                <li><span><b>Откройте кабинет и нажмите «Добавить в Happ»</b> в блоке «Подключение» — подписка добавится сама.</span></li>
                <li><span><b>Включите подключение переключателем в Happ.</b></span></li>
              </ol>
              <div className="row wrap gap-2" style={{ marginTop: 20 }}>
                {[HAPP.ios, HAPP.android].map((a) => <Button key={a.url} variant="secondary" href={a.url} block={narrow}>{a.label} · {a.store}</Button>)}
                <Button variant="secondary" href={HAPP.desktop.url} block={narrow}>Windows, macOS и Linux</Button>
              </div>
            </section>

            <section id="mobile" className="card" style={{ padding: narrow ? 22 : 30 }}>
              <h2 className="t-h2">Не работает на мобильном интернете</h2>
              <ol className="num">
                <li><span><b>Обновите подписку в Happ</b> — потяните список вниз или нажмите «Обновить». Адреса для мобильных сетей приходят с обновлением.</span></li>
                <li><span><b>Попробуйте другую строку подписки.</b> Если одна не соединяется, Happ позволяет выбрать соседнюю.</span></li>
                <li><span><b>Проверьте, работает ли по Wi-Fi.</b> Если да — напишите нам оператора и город, проверим вход для вашей сети.</span></li>
              </ol>
            </section>

            <section id="expired" className="card row gap-6" style={{ padding: narrow ? 22 : 30, alignItems: 'center' }}>
              {!narrow && <Hedgehog mood="sleep" size={110} style={{ flex: 'none' }} />}
              <div style={{ flex: 1 }}>
                <h2 className="t-h2">Подписка закончилась или не продлилась</h2>
                <p className="muted" style={{ marginTop: 8 }}>Автопродление списывает деньги с кошелька, только если их хватает. Пополните кошелёк или выберите тариф заново — подписка включится сразу после оплаты.</p>
                <div className="row wrap gap-2" style={{ marginTop: 16 }}>
                  <Button variant="primary" to={guard('/payment')}>Выбрать тариф</Button>
                  <Button variant="ghost" to={guard('/wallet')}>Пополнить кошелёк</Button>
                </div>
              </div>
            </section>

            <section id="lost-id" className="card" style={{ padding: narrow ? 22 : 30 }}>
              <h2 className="t-h2">Потерял ID или пароль</h2>
              <p className="muted" style={{ marginTop: 10, maxWidth: 680 }}>
                ID — единственный ключ входа, поэтому восстановить его без пароля нельзя. Если вы задавали логин и пароль — <Link to="/login" className="link">войдите по логину</Link>. Если нет — напишите в поддержку и укажите дату и сумму оплаты: найдём аккаунт по платежу.
              </p>
              <p className="muted" style={{ marginTop: 10, maxWidth: 680 }}>
                Забыли пароль — напишите в поддержку и назовите HiEarth ID или логин. Пароль сбросят и выдадут временный (если почта подтверждена, он придёт и письмом). После входа кабинет попросит придумать свой пароль.
              </p>
            </section>

            <section id="refund" className="card" style={{ padding: narrow ? 22 : 30 }}>
              <h2 className="t-h2">Как вернуть деньги</h2>
              <p className="muted" style={{ marginTop: 10 }}>
                Условия и порядок — на странице <Link to="/refund" className="link">«Условия возврата»</Link>. Заявку можно отправить из раздела {isLoggedIn ? <Link to="/payments" className="link">«Оплата и подписка»</Link> : '«Оплата и подписка»'} в кабинете.
              </p>
            </section>

            <section id="support" className="card card--elevated" style={{ padding: narrow ? 22 : 30 }}>
              <h2 className="t-h2">Связаться с поддержкой</h2>
              <div className="row wrap gap-3" style={{ marginTop: 14, alignItems: 'center' }}>
                <Button variant="secondary" icon="mail" to={isLoggedIn ? "/support" : "/login?redirect=/support"}>Написать в поддержку</Button>
                <span className="t-sm muted">Ответим в кабинете и в Telegram — обычно в течение дня.</span>
              </div>
              {!loaded ? (
                <Skeleton w={220} h={16} style={{ marginTop: 14 }} />
              ) : contact || email ? (
                <div className="row wrap gap-2" style={{ marginTop: 14 }}>
                  {contact && (contactLink
                    ? <Button variant="secondary" href={contactLink} icon="send" target={contactLink.startsWith('http') ? '_blank' : undefined} rel="noopener noreferrer">Написать: {contact}</Button>
                    : <span className="mono">{contact}</span>)}
                  {email && <Button variant="ghost" href={`mailto:${email}`} icon="mail">{email}</Button>}
                </div>
              ) : (
                <p className="muted" style={{ marginTop: 10, maxWidth: 640 }}>Контакты поддержки скоро появятся здесь. Пока опишите проблему через раздел «Документы» — там указаны реквизиты.</p>
              )}
              {!isLoggedIn && <Alert tone="info" title="Укажите свой ID" className="mt-4">— он в личном кабинете. Так мы быстрее найдём аккаунт.</Alert>}
            </section>
          </div>
        </div>
      </main>
      <SiteFooter compact />
    </>
  )
}
