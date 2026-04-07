import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Link } from 'react-router-dom'
import { AuthProvider } from './auth/AuthContext.jsx'
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

function NotFoundPage() {
  return (
    <div className="flex flex-col items-center justify-center py-32 px-4">
      <h1 className="text-6xl font-bold text-white/20 mb-4">404</h1>
      <p className="text-lg text-white/60 mb-6">Страница не найдена</p>
      <Link to="/" className="px-5 py-2.5 bg-brand rounded-xl text-white hover:bg-brand/80 transition">
        На главную
      </Link>
    </div>
  )
}

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
      <div className="min-h-screen flex flex-col">
        <SiteHeader />
        <main className="flex-1">
          <Routes>
            <Route path="/" element={<LandingPage />} />
            <Route path="/business" element={<BusinessPage />} />
            <Route path="/business/signup" element={<BusinessSignupPage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/register" element={<RegisterPage />} />
            <Route path="/privacy" element={<PrivacyPage />} />
            <Route path="/refund" element={<RefundPage />} />
            <Route path="/offer" element={<OfferPage />} />
            <Route path="/dashboard" element={<RequireAuth><DashboardPage /></RequireAuth>} />
            <Route path="/payment" element={<RequireAuth><PaymentPage /></RequireAuth>} />
            <Route path="/settings" element={<RequireAuth><SettingsPage /></RequireAuth>} />
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
