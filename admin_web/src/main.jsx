import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './auth/AuthContext.jsx'
import { I18nProvider } from './i18n/index.jsx'
import { ToastProvider } from './components/Toast.jsx'
import ErrorBoundary from './components/ErrorBoundary.jsx'
import RequireAuth from './auth/RequireAuth.jsx'
import LoginPage from './auth/LoginPage.jsx'
import Layout from './components/Layout.jsx'
import DashboardPage from './pages/DashboardPage.jsx'
import UsersPage from './pages/UsersPage.jsx'
import GroupsPage from './pages/GroupsPage.jsx'
import SubscriptionsPage from './pages/SubscriptionsPage.jsx'
import PromocodesPage from './pages/PromocodesPage.jsx'
import PaymentsPage from './pages/PaymentsPage.jsx'
import ServersPage from './pages/ServersPage.jsx'
import ServerDetailPage from './pages/ServerDetailPage.jsx'
import SshKeysPage from './pages/SshKeysPage.jsx'
import ReleasesPage from './pages/ReleasesPage.jsx'
import AdminsPage from './pages/AdminsPage.jsx'
import TenantsPage from './pages/TenantsPage.jsx'
import TrafficMaskingPage from './pages/TrafficMaskingPage.jsx'
import TrafficPage from './pages/TrafficPage.jsx'
import AuditLogPage from './pages/AuditLogPage.jsx'
import SettingsPage from './pages/SettingsPage.jsx'
import AutoTunerPage from './pages/AutoTunerPage.jsx'
import PlansPage from './pages/PlansPage.jsx'
import './style.css'

createRoot(document.getElementById('root')).render(
  <StrictMode>
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
                  <Route path="/groups" element={<GroupsPage />} />
                  <Route path="/subscriptions" element={<SubscriptionsPage />} />
                  <Route path="/promocodes" element={<PromocodesPage />} />
                  <Route path="/payments" element={<PaymentsPage />} />
                  <Route path="/traffic" element={<TrafficPage />} />
                  <Route path="/servers" element={<ServersPage />} />
                  <Route path="/servers/:id" element={<ServerDetailPage />} />
                  <Route path="/ssh-keys" element={<SshKeysPage />} />
                  <Route path="/releases" element={<ReleasesPage />} />
                  <Route path="/admins" element={<AdminsPage />} />
                  <Route path="/tenants" element={<TenantsPage />} />
                  <Route path="/traffic-masking" element={<TrafficMaskingPage />} />
                  <Route path="/audit-log" element={<AuditLogPage />} />
                  <Route path="/settings" element={<SettingsPage />} />
                  <Route path="/auto-tuner" element={<AutoTunerPage />} />
                  <Route path="/plans" element={<PlansPage />} />
                  <Route path="*" element={<Navigate to="/dashboard" replace />} />
                </Route>
              </Route>
            </Routes>
            </ToastProvider>
          </AuthProvider>
        </I18nProvider>
      </BrowserRouter>
    </ErrorBoundary>
  </StrictMode>
)
