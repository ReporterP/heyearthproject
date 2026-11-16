import SiteHeader from '../components/SiteHeader.jsx'
import SiteFooter from '../components/SiteFooter.jsx'

// TEMPORARY: frame for a page that is not rebuilt on «Канон» yet (new header and footer, the old body
// in the .legacy compatibility layer). Removed with styles/legacy.css when the last page is rebuilt.
export default function LegacyLayout({ children }) {
  return (
    <>
      <SiteHeader />
      <main id="main" className="legacy">{children}</main>
      <SiteFooter />
    </>
  )
}
