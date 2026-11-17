import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import '@fontsource/ibm-plex-mono/700.css'
import '@fontsource-variable/jetbrains-mono'
import { AuthProvider } from './auth/AuthContext.jsx'
import { I18nProvider } from './i18n/index.jsx'
import { ThemeProvider } from './theme/ThemeContext.jsx'
import { ToastProvider } from './components/Toast.jsx'
import ErrorBoundary from './components/ErrorBoundary.jsx'
import RequireAuth from './auth/RequireAuth.jsx'
import LoginPage from './auth/LoginPage.jsx'
import Layout from './components/Layout.jsx'
import DashboardPage from './pages/DashboardPage.jsx'
import UsersPage from './pages/UsersPage.jsx'
import PromocodesPage from './pages/PromocodesPage.jsx'
import PaymentsPage from './pages/PaymentsPage.jsx'
import ReferralsPage from './pages/ReferralsPage.jsx'
import ServersPage from './pages/ServersPage.jsx'
import { NodesRedirect, EntriesRedirect, ServerIdRedirect } from './pages/ServersRedirects.jsx'
import AdminsPage from './pages/AdminsPage.jsx'
import TenantsPage from './pages/TenantsPage.jsx'
import AuditLogPage from './pages/AuditLogPage.jsx'
import SettingsPage from './pages/SettingsPage.jsx'
import PlansPage from './pages/PlansPage.jsx'
import B2BAnalyticsPage from './pages/B2BAnalyticsPage.jsx'
import MTProtoPage from './pages/MTProtoPage.jsx'
import DirectRoutesPage from './pages/DirectRoutesPage.jsx'
import AlertsPage from './pages/AlertsPage.jsx'
import SupportPage from './pages/SupportPage.jsx'
import './style.css'

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <ThemeProvider>
      <ErrorBoundary>
        <BrowserRouter>
          <I18nProvider>
            <AuthProvider>
              <ToastProvider>
                <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route element={<RequireAuth />}>
                <Route element={<Layout />}>
                  <Route path="/dashboard" element={<DashboardPage />} />
                  <Route path="/users" element={<UsersPage />} />
                  <Route path="/promocodes" element={<PromocodesPage />} />
                  <Route path="/payments" element={<PaymentsPage />} />
                  <Route path="/payment-ops" element={<Navigate to="/payments?tab=emails" replace />} />
                  <Route path="/referrals" element={<ReferralsPage />} />
                  <Route path="/servers" element={<ServersPage />} />
                  <Route path="/servers/:id" element={<ServerIdRedirect />} />
                  <Route path="/nodes" element={<NodesRedirect />} />
                  <Route path="/entries" element={<EntriesRedirect />} />
                  <Route path="/admins" element={<AdminsPage />} />
                  <Route path="/tenants" element={<TenantsPage />} />
                  <Route path="/b2b-analytics" element={<B2BAnalyticsPage />} />
                  <Route path="/audit-log" element={<AuditLogPage />} />
                  <Route path="/settings" element={<SettingsPage />} />
                  <Route path="/plans" element={<PlansPage />} />
                  <Route path="/mtproto" element={<MTProtoPage />} />
                  <Route path="/alerts" element={<AlertsPage />} />
                  <Route path="/support" element={<SupportPage />} />
                  <Route path="/direct-routes" element={<DirectRoutesPage />} />
                  <Route path="*" element={<Navigate to="/dashboard" replace />} />
                </Route>
              </Route>
            </Routes>
            </ToastProvider>
          </AuthProvider>
        </I18nProvider>
      </BrowserRouter>
      </ErrorBoundary>
    </ThemeProvider>
  </StrictMode>
)
