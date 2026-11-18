import SiteHeader from '../components/SiteHeader.jsx'
import SiteFooter from '../components/SiteFooter.jsx'
import Badge from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import { Progress } from '../components/Controls.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { B2B } from '../lib/b2b.js'
import { group } from '../lib/format.js'

// Для бизнеса (spec 10 §10.1, artboard «Для бизнеса»). Behind b2b_enabled (B2BOnly in main.jsx).
const FEATURES = [
  ['Своя панель управления', 'Сотрудники, роли, баланс и счета компании в одном месте.'],
  ['Свои или наши серверы', 'Подключите собственную инфраструктуру или используйте наши узлы.'],
  ['Оплата за места', 'Раз в месяц с кошелька компании — за столько мест, сколько вы выбрали.'],
  ['Доступ по приглашению', 'Сотрудник открывает ссылку — нужен только HiEarth ID, он выдаётся анонимно в один клик.'],
]

const SAMPLE = [
  ['АК', 'Анна К.', 'администратор', 'info', 'Админ'],
  ['ИП', 'Игорь П.', 'бухгалтерия', 'warn', 'Биллинг'],
  ['МС', 'Мария С.', 'подключена 2 дня назад', undefined, 'Сотрудник'],
]

export default function BusinessPage() {
  const narrow = useMedia('(max-width: 1024px)')
  const phone = useMedia('(max-width: 640px)')
  usePageMeta('HiEarth для бизнеса', { description: 'Защищённое подключение для сотрудников компании: доступ по приглашению, роли, кошелёк компании, счета для юрлиц.' })
  const ctas = (lg = true) => (
    <>
      <Button variant="primary" size={lg ? 'lg' : undefined} block={phone} to="/business/signup">Подключить бизнес</Button>
      <Button variant="secondary" size={lg ? 'lg' : undefined} block={phone} to="/business/invoice">Получить счёт на оплату</Button>
    </>
  )
  return (
    <>
      <SiteHeader variant="business" />
      <main id="main">
        <section style={{ padding: phone ? '32px 0 56px' : '72px 0 96px' }}>
          <div className="container grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : 'minmax(0, 1fr) minmax(0, 1fr)', gap: narrow ? 40 : 56, alignItems: 'center' }}>
            <div>
              <h1 className="t-d1 rise">HiEarth для&nbsp;команд</h1>
              <p className="t-lead rise" style={{ '--d': '120ms', marginTop: 20, maxWidth: 520 }}>Защищённое подключение для всех сотрудников. Сотруднику нужен только HiEarth ID — он выдаётся анонимно в один клик, а доступ оплачивает компания. Роли, кошелёк компании, счета для юрлиц.</p>
              <div className={`${phone ? 'stack' : 'row wrap'} gap-3 rise`} style={{ '--d': '200ms', marginTop: 34 }}>{ctas()}</div>
              <p className="t-sm subtle rise" style={{ '--d': '280ms', marginTop: 20 }}>Без предоплаты на сайте. Оплата — после входа в панель управления.</p>
            </div>
            <div className="card card--elevated rise" style={{ '--d': '200ms', padding: 26, borderRadius: 26 }} role="img" aria-label="Пример панели компании, данные условные">
              <div aria-hidden="true">
                <div className="row between"><span><b>ООО «Пример»</b><br /><span className="t-xs subtle">ИНН 0000000000 · пример</span></span><Badge tone="ok">Активен</Badge></div>
                <div className="row between" style={{ marginTop: 20 }}><span className="t-sm muted">Мест занято</span><span className="mono t-sm">8 из 10</span></div>
                <div style={{ marginTop: 8 }}><Progress value={80} /></div>
                <div style={{ marginTop: 18 }}>
                  {SAMPLE.map(([ini, name, note, tone, role], i) => (
                    <div key={name} style={{ display: 'grid', gridTemplateColumns: '32px minmax(0,1fr) auto', gap: 12, alignItems: 'center', padding: '10px 0', borderTop: i ? '1px solid var(--line)' : 0 }}>
                      <span className="ava">{ini}</span>
                      <span><b className="t-sm">{name}</b><br /><span className="t-xs subtle">{note}</span></span>
                      <Badge tone={tone}>{role}</Badge>
                    </div>
                  ))}
                </div>
                <span className="btn btn--secondary btn--block" style={{ marginTop: 14, pointerEvents: 'none' }}>+ Пригласить сотрудника</span>
              </div>
              <p className="t-xs subtle" style={{ marginTop: 10, textAlign: 'center' }}>Пример панели, данные условные</p>
            </div>
          </div>
        </section>

        <section style={{ padding: phone ? '48px 0' : '88px 0', borderTop: '1px solid var(--line)' }}>
          <div className="container">
            <h2 className="t-d2">Всё для корпоративного доступа</h2>
            <div className="grid g-2" style={{ gap: 16, marginTop: phone ? 24 : 44 }}>
              {FEATURES.map(([t, d]) => <article key={t} className="card" style={{ padding: 28 }}><h3 className="t-h3">{t}</h3><p className="muted" style={{ marginTop: 8 }}>{d}</p></article>)}
            </div>
          </div>
        </section>

        <section style={{ padding: phone ? '48px 0' : '88px 0', borderTop: '1px solid var(--line)' }}>
          <div className="container">
            <div className="row wrap between" style={{ alignItems: 'flex-end', gap: 16 }}><h2 className="t-d2">Прозрачные цены</h2><p className="muted" style={{ maxWidth: 420 }}>Цены настраиваются индивидуально. Указаны базовые значения.</p></div>
            <div className="grid g-3" style={{ marginTop: phone ? 24 : 40 }}>
              <div className="plan"><span className="plan__name">Пользователь</span><span className="plan__term">за сотрудника</span><span className="plan__price">{B2B.perUser}&nbsp;₽</span><span className="plan__per">в месяц</span></div>
              <div className="plan"><span className="plan__name">Наши серверы</span><span className="plan__term">за компанию</span><span className="plan__price">{group(B2B.ourServers)}&nbsp;₽</span><span className="plan__per">в месяц</span></div>
              <div className="plan"><span className="plan__name">Свои серверы</span><span className="plan__term">до {B2B.includedServers} включены</span><span className="plan__price">+{B2B.perExtraServer}&nbsp;₽</span><span className="plan__per">за каждый дополнительный</span></div>
            </div>
            <div className={`card card--glow ${phone ? 'stack' : 'row wrap between'}`} style={{ marginTop: 32, padding: phone ? 24 : '36px 40px', borderRadius: 24, gap: 20 }}>
              <h3 className="t-h1">Готовы начать?</h3>
              <div className={`${phone ? 'stack' : 'row'} gap-2`}>
                <Button variant="primary" size="lg" block={phone} to="/business/signup">Подключить бизнес</Button>
                <Button variant="secondary" size="lg" block={phone} to="/business/invoice">Получить счёт</Button>
              </div>
            </div>
          </div>
        </section>
      </main>
      <SiteFooter compact />
    </>
  )
}
