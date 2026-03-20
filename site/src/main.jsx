import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
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

function App() {
  return (
    <BrowserRouter>
      <div className="min-h-screen flex flex-col">
        <SiteHeader />
        <main className="flex-1">
          <Routes>
            <Route path="/" element={<LandingPage />} />
            <Route path="/business" element={<BusinessPage />} />
            <Route path="/business/signup" element={<BusinessSignupPage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/register" element={<RegisterPage />} />
            <Route path="/dashboard" element={<RequireAuth><DashboardPage /></RequireAuth>} />
            <Route path="/payment" element={<RequireAuth><PaymentPage /></RequireAuth>} />
            <Route path="/settings" element={<RequireAuth><SettingsPage /></RequireAuth>} />
          </Routes>
        </main>
        <SiteFooter />
      </div>
    </BrowserRouter>
  )
}

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <App />
  </StrictMode>
)
