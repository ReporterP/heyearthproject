import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './auth/AuthContext.jsx'
import { I18nProvider } from './i18n/index.jsx'
import { ToastProvider } from './components/Toast.jsx'
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
import SshKeysPage from './pages/SshKeysPage.jsx'
import ReleasesPage from './pages/ReleasesPage.jsx'
import AdminsPage from './pages/AdminsPage.jsx'
import TenantsPage from './pages/TenantsPage.jsx'
import TrafficMaskingPage from './pages/TrafficMaskingPage.jsx'
import SettingsPage from './pages/SettingsPage.jsx'
import './style.css'

createRoot(document.getElementById('root')).render(
  <StrictMode>
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
                  <Route path="/servers" element={<ServersPage />} />
                  <Route path="/ssh-keys" element={<SshKeysPage />} />
                  <Route path="/releases" element={<ReleasesPage />} />
                  <Route path="/admins" element={<AdminsPage />} />
                  <Route path="/tenants" element={<TenantsPage />} />
                  <Route path="/traffic-masking" element={<TrafficMaskingPage />} />
                  <Route path="/settings" element={<SettingsPage />} />
                  <Route path="*" element={<Navigate to="/dashboard" replace />} />
                </Route>
              </Route>
            </Routes>
          </ToastProvider>
        </AuthProvider>
      </I18nProvider>
    </BrowserRouter>
  </StrictMode>
)
