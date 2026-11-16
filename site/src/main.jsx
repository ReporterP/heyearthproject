import { StrictMode, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Navigate, useLocation, useNavigationType, useSearchParams } from 'react-router-dom'
import '@fontsource-variable/manrope'
import '@fontsource/jetbrains-mono/500.css'
import '@fontsource/jetbrains-mono/600.css'
import '@fontsource/jetbrains-mono/700.css'
import './styles/site.css'
import { AuthProvider } from './auth/AuthContext.jsx'
import { ToastProvider } from './components/Toast.jsx'
import RequireAuth from './components/RequireAuth.jsx'
import LegacyLayout from './legacy/LegacyLayout.jsx'
import useSiteInfo from './hooks/useSiteInfo.js'
import NotFoundPage from './pages/NotFoundPage.jsx'
import LandingPage from './pages/LandingPage.jsx'
import LoginPage from './pages/LoginPage.jsx'
import RegisterPage from './pages/RegisterPage.jsx'
import DashboardPage from './pages/DashboardPage.jsx'
import PaymentPage from './pages/PaymentPage.jsx'
import SettingsPage from './pages/SettingsPage.jsx'
import BusinessPage from './pages/BusinessPage.jsx'
import BusinessSignupPage from './pages/BusinessSignupPage.jsx'
import PrivacyPage from './pages/PrivacyPage.jsx'
import RefundPage from './pages/RefundPage.jsx'
import OfferPage from './pages/OfferPage.jsx'
import PaymentsHistoryPage from './pages/PaymentsHistoryPage.jsx'
import ConnectionsPage from './pages/ConnectionsPage.jsx'
import CheckoutPage from './pages/CheckoutPage.jsx'
import PaymentDetailPage from './pages/PaymentDetailPage.jsx'
import BusinessInvoicePage from './pages/BusinessInvoicePage.jsx'
import BusinessDashboardPage from './pages/BusinessDashboardPage.jsx'
import ClaimCodePage from './pages/ClaimCodePage.jsx'
import ConnectPage from './pages/ConnectPage.jsx'
import WalletPage from './pages/WalletPage.jsx'
import ReferralLinkPage from './pages/ReferralLinkPage.jsx'
import HelpPage from './pages/HelpPage.jsx'
import { EmailConfirmPage, EmailUnsubscribePage } from './pages/EmailLinkPage.jsx'

const L = (el) => <LegacyLayout>{el}</LegacyLayout>

// /connect is the one connection page: with ?sub=<token> it is the link the app and the e-mails
// open (no login: the token is the key), without it the cabinet's page.
function ConnectRoute() {
  const [params] = useSearchParams()
  if (params.get('sub') !== null) return <ConnectPage />
  return <RequireAuth><ConnectionsPage /></RequireAuth>
}

// B2BOnly hides the business pages while b2b_enabled is off: they answer like any unknown page.
function B2BOnly({ children }) {
  const { info, loaded } = useSiteInfo()
  if (!loaded) return null
  return info.b2b_enabled ? children : <NotFoundPage />
}

// Scroll: a new page starts at the top; an anchor (/#plans, /help#mobile) scrolls to its section,
// waiting a little for content that loads its data first.
// On the first load (a link like /help#mobile from outside) the jump is instant and repeated once
// the page has settled (fonts, data), since a smooth scroll started that early gets cancelled.
function ScrollManager() {
  const { pathname, hash, key } = useLocation()
  const navType = useNavigationType()
  useEffect(() => {
    const initial = key === 'default' // the location the page was opened with
    if (!hash) {
      // «Назад/Вперёд» keep the browser's own scroll position
      if (navType !== 'POP' || initial) window.scrollTo({ top: 0, left: 0, behavior: 'instant' })
      return undefined
    }
    let tries = 0
    const timers = []
    const go = (behavior) => {
      let id = hash.slice(1)
      try { id = decodeURIComponent(id) } catch { /* keep raw */ }
      const el = document.getElementById(id)
      if (el) { el.scrollIntoView({ block: 'start', behavior }); return true }
      if (tries++ < 60) timers.push(setTimeout(() => go(behavior), 50))
      return false
    }
    go(initial ? 'instant' : 'smooth')
    if (initial) timers.push(setTimeout(() => go('instant'), 700))
    return () => timers.forEach(clearTimeout)
  }, [pathname, hash, key]) // eslint-disable-line react-hooks/exhaustive-deps
  return null
}

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <ToastProvider>
          <a className="skip" href="#main">Перейти к содержимому</a>
          <ScrollManager />
          <Routes>
            <Route path="/" element={<LandingPage />} />
            <Route path="/help" element={<HelpPage />} />
            <Route path="/offer" element={<OfferPage />} />
            <Route path="/privacy" element={<PrivacyPage />} />
            <Route path="/refund" element={<RefundPage />} />
            <Route path="/r/:code" element={<ReferralLinkPage />} />
            <Route path="/email/confirm" element={<EmailConfirmPage />} />
            <Route path="/email/unsubscribe" element={<EmailUnsubscribePage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/register" element={<RegisterPage />} />

            <Route path="/business" element={<B2BOnly>{L(<BusinessPage />)}</B2BOnly>} />
            <Route path="/business/signup" element={<B2BOnly>{L(<BusinessSignupPage />)}</B2BOnly>} />
            <Route path="/business/invoice" element={<B2BOnly>{L(<BusinessInvoicePage />)}</B2BOnly>} />
            <Route path="/business/dashboard" element={<B2BOnly><RequireAuth>{L(<BusinessDashboardPage />)}</RequireAuth></B2BOnly>} />
            <Route path="/claim/:code" element={<B2BOnly>{L(<ClaimCodePage />)}</B2BOnly>} />

            <Route path="/connect" element={<ConnectRoute />} />
            <Route path="/connections" element={<Navigate to="/connect" replace />} />
            <Route path="/dashboard" element={<RequireAuth><DashboardPage /></RequireAuth>} />
            <Route path="/settings" element={<RequireAuth><SettingsPage /></RequireAuth>} />
            <Route path="/payment" element={<RequireAuth>{L(<PaymentPage />)}</RequireAuth>} />
            <Route path="/checkout/:paymentId" element={<RequireAuth>{L(<CheckoutPage />)}</RequireAuth>} />
            {/* Old links (Happ's «Продлите» before 13.11) pointed at /checkout without an id */}
            <Route path="/checkout" element={<Navigate to="/payment" replace />} />
            <Route path="/wallet" element={<RequireAuth>{L(<WalletPage />)}</RequireAuth>} />
            <Route path="/payments" element={<RequireAuth>{L(<PaymentsHistoryPage />)}</RequireAuth>} />
            <Route path="/payments/:id" element={<RequireAuth>{L(<PaymentDetailPage />)}</RequireAuth>} />
            <Route path="*" element={<NotFoundPage />} />
          </Routes>
          <div id="he-layer" />
        </ToastProvider>
      </AuthProvider>
    </BrowserRouter>
  )
}

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <div className="he">
      <App />
    </div>
  </StrictMode>,
)
