import { Link } from 'react-router-dom'
import useLegalInfo from '../hooks/useLegalInfo.js'
import useSiteInfo from '../hooks/useSiteInfo.js'
import { LogoMark } from './Logo.jsx'

// SiteFooter (spec 02 §31). Full on public pages; `compact` — one line of documents (help, legal,
// B2B landing). The seller's details come from /api/v1/legal-info when the API has them.
export default function SiteFooter({ compact = false }) {
  const legal = useLegalInfo()
  const { info } = useSiteInfo()
  const docs = (
    <>
      <Link to="/offer">Договор-оферта</Link>
      <Link to="/privacy">Конфиденциальность</Link>
      <Link to="/refund">Условия возврата</Link>
    </>
  )
  if (compact) {
    return (
      <footer className="site-foot">
        <div className="container row wrap between gap-4">
          <span>© HiEarth{legal.legal_company_name ? ` · ${legal.legal_company_name}` : ''}</span>
          <span className="row wrap gap-6">{docs}</span>
        </div>
      </footer>
    )
  }
  return (
    <footer className="site-foot">
      <div className="container grid gap-8 grid-cols-1 sm:grid-cols-2 lg:grid-cols-[2fr_1fr_1fr_1fr]">
        <div>
          <Link to="/" className="logo" aria-label="HiEarth — на главную"><LogoMark check={false} /><span aria-hidden="true">HiEarth</span></Link>
          <p style={{ marginTop: 12, maxWidth: 280 }}>Защищённое подключение к интернету. Оплата через ЮKassa, данные карты у нас не хранятся.</p>
          {(legal.legal_company_name || legal.legal_inn || legal.legal_email) && (
            <p className="t-xs subtle" style={{ marginTop: 12 }}>
              {legal.legal_company_name}{legal.legal_inn ? `${legal.legal_company_name ? ' · ' : ''}ИНН ${legal.legal_inn}` : ''}
              {legal.legal_email && <><br /><a href={`mailto:${legal.legal_email}`}>{legal.legal_email}</a></>}
            </p>
          )}
        </div>
        <nav className="stack gap-2" aria-label="Продукт">
          <b style={{ color: 'var(--fg)' }}>Продукт</b>
          <Link to="/#plans">Тарифы</Link>
          <Link to="/help">Помощь</Link>
          {info.b2b_enabled && <Link to="/business">Для бизнеса</Link>}
          <Link to="/dashboard">Личный кабинет</Link>
        </nav>
        <nav className="stack gap-2" aria-label="Документы">
          <b style={{ color: 'var(--fg)' }}>Документы</b>
          {docs}
        </nav>
        <div className="stack gap-2">
          <b style={{ color: 'var(--fg)' }}>Оплата</b>
          <span>Карты и СБП</span>
          <span>через ЮKassa</span>
        </div>
      </div>
    </footer>
  )
}
