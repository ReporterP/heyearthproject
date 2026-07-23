import { StrictMode, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom'
import { AuthProvider } from './auth/AuthContext.jsx'
import '@fontsource-variable/lora'
import '@fontsource-variable/raleway'
import './style.css'
import SiteHeader from './components/SiteHeader.jsx'
import SiteFooter from './components/SiteFooter.jsx'
import RequireAuth from './components/RequireAuth.jsx'
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

function NotFoundPage() {
  return (
    <div className="relative flex flex-col items-center justify-center py-40 px-4 text-center overflow-hidden">
      <div className="pointer-events-none absolute left-1/2 top-1/3 h-72 w-72 -translate-x-1/2 rounded-full bg-brand/10 blur-3xl" />
      <p className="serif text-[7rem] leading-none font-semibold text-brand/25 tabular-nums">404</p>
      <h1 className="serif text-2xl mt-2 mb-3">Тропа не найдена</h1>
      <p className="text-text-secondary mb-8 max-w-sm">
        Похоже, эта страница затерялась в лесу. Вернёмся к началу.
      </p>
      <Link to="/" className="btn-primary">На главную</Link>
    </div>
  )
}

function ScrollToTop() {
  const { pathname } = useLocation()
  useEffect(() => { window.scrollTo(0, 0) }, [pathname])
  return null
}

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
      <ScrollToTop />
      <div className="min-h-screen flex flex-col">
        <SiteHeader />
        <main className="flex-1">
          <Routes>
            <Route path="/" element={<LandingPage />} />
            <Route path="/business" element={<BusinessPage />} />
            <Route path="/business/signup" element={<BusinessSignupPage />} />
            <Route path="/business/invoice" element={<BusinessInvoicePage />} />
            {/* B2B self-service dashboard — requires login + tenant membership */}
            <Route path="/business/dashboard" element={<RequireAuth><BusinessDashboardPage /></RequireAuth>} />
            {/* Claim a tenant invite code — auth-gated inside the page (it stashes intent and redirects to /login) */}
            <Route path="/claim/:code" element={<ClaimCodePage />} />
            {/* Happ deeplink handler — public route (no auth) — token in ?sub= is itself the bearer */}
            <Route path="/connect" element={<ConnectPage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/register" element={<RegisterPage />} />
            <Route path="/privacy" element={<PrivacyPage />} />
            <Route path="/refund" element={<RefundPage />} />
            <Route path="/offer" element={<OfferPage />} />
            <Route path="/dashboard" element={<RequireAuth><DashboardPage /></RequireAuth>} />
            <Route path="/payment" element={<RequireAuth><PaymentPage /></RequireAuth>} />
            <Route path="/checkout/:paymentId" element={<RequireAuth><CheckoutPage /></RequireAuth>} />
            <Route path="/settings" element={<RequireAuth><SettingsPage /></RequireAuth>} />
            <Route path="/payments" element={<RequireAuth><PaymentsHistoryPage /></RequireAuth>} />
            <Route path="/payments/:id" element={<RequireAuth><PaymentDetailPage /></RequireAuth>} />
            <Route path="/connections" element={<RequireAuth><ConnectionsPage /></RequireAuth>} />
            <Route path="*" element={<NotFoundPage />} />
          </Routes>
        </main>
        <SiteFooter />
      </div>
      </AuthProvider>
    </BrowserRouter>
  )
}

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <App />
  </StrictMode>
)
