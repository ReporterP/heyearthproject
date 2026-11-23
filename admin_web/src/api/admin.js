import { apiFetch, pagedFetch } from './client.js'

// ── Auth ──
export function login(username, password, totpCode) {
  // A rejected login (401/403) shows the server's reason and must not run the logout path.
  // With 2FA on the server answers 401 + totp_required until the code from the app is sent.
  const body = totpCode ? { username, password, totp_code: totpCode } : { username, password }
  return apiFetch('/login', { method: 'POST', body, sessionErrors: false })
}

// ── Dashboard ──
export function fetchOverview() {
  return apiFetch('/overview')
}
export function fetchCapacity() {
  return apiFetch('/capacity')
}

// ── Users ──
export function fetchUsersPaged(params) {
  return pagedFetch('/users', params)
}
export function updateUser(id, payload) {
  return apiFetch(`/users/${id}`, { method: 'PATCH', body: payload })
}
export function deleteUser(id) {
  return apiFetch(`/users/${id}`, { method: 'DELETE' })
}
export function fetchUserSubscriptions(userDbId) {
  return pagedFetch('/subscriptions', { user_db_id: userDbId, page: 1, pageSize: 100, sort: 'valid_until', dir: 'desc' })
}
export function fetchUserPayments(userDbId) {
  return pagedFetch('/payments', { user_db_id: userDbId, page: 1, pageSize: 50, sort: 'created_at', dir: 'desc' })
}
export function fetchUserTraffic(userDbId) {
  return apiFetch(`/users/${userDbId}/traffic`)
}

export function saveSubscription(payload) {
  return apiFetch('/subscriptions', { method: 'POST', body: payload })
}
export function updateSubscription(id, payload) {
  return apiFetch(`/subscriptions/${id}`, { method: 'PATCH', body: payload })
}
export function deleteSubscription(id) {
  return apiFetch(`/subscriptions/${id}`, { method: 'DELETE' })
}

// ── Promocodes ──
export function fetchPromocodesPaged(params) {
  return pagedFetch('/promocodes', params)
}
export function createPromocode(payload) {
  return apiFetch('/promocodes', { method: 'POST', body: payload })
}
export function updatePromocode(code, payload) {
  return apiFetch(`/promocodes/${encodeURIComponent(code)}`, { method: 'PATCH', body: payload })
}
export function deletePromocode(code) {
  return apiFetch(`/promocodes/${encodeURIComponent(code)}`, { method: 'DELETE' })
}
export function reapplyPromocodePlan(code) {
  return apiFetch(`/promocodes/${encodeURIComponent(code)}/reapply-plan`, { method: 'POST' })
}

// ── Payments ──
export function fetchPaymentsPaged(params) {
  return pagedFetch('/payments', params)
}
// Ask YooKassa for the status of every pending payment (the background sync, now).
export const syncPendingPayments = () => apiFetch('/sync-pending-payments', { method: 'POST' })
export function createPayment(payload) {
  return apiFetch('/payments', { method: 'POST', body: payload })
}
export function updatePayment(id, payload) {
  return apiFetch(`/payments/${id}`, { method: 'PATCH', body: payload })
}
export function cancelPayment(id, reason = '', amount = 0) {
  return apiFetch(`/payments/${id}/cancel`, { method: 'POST', body: { reason, amount } })
}
export function checkPaymentStatus(id) {
  return apiFetch(`/payments/${id}/check-status`, { method: 'POST' })
}

// ── Servers ──
export function fetchServersPaged(params) {
  return pagedFetch('/servers', params)
}
export function fetchHysteria2Status() {
  return apiFetch('/hysteria2/status')
}
export function runServerAction(id, action) {
  return apiFetch(`/servers/${id}/actions`, { method: 'POST', body: { action } })
}

// ── Nodes: provisioning core (jobs survive an API restart; reads servers.read, changes nodes.provision) ──
export const fetchNodes = (role) => apiFetch(`/nodes?role=${encodeURIComponent(role || '')}`)
export const probeNode = (payload) => apiFetch('/servers/probe', { method: 'POST', body: payload })
export const createNode = (payload) => apiFetch('/servers', { method: 'POST', body: payload })
export const confirmHostKey = (id, fingerprint) => apiFetch(`/servers/${id}/host-key`, { method: 'POST', body: { fingerprint } })
export const preflightNode = (id) => apiFetch(`/servers/${id}/preflight`, { method: 'POST' })
export const provisionNode = (id, options = {}) => apiFetch(`/servers/${id}/provision`, { method: 'POST', body: options })
export const verifyNode = (id) => apiFetch(`/servers/${id}/verify`, { method: 'POST' })
// confirmLast: «ОТКЛЮЧИТЬ ВСЕХ» when this is the only working exit (B4; the API answers 409 without it).
export const deprovisionNode = (id, confirmName, confirmLast = '') =>
  apiFetch(`/servers/${id}/deprovision`, { method: 'POST', body: { confirm_name: confirmName, confirm_last: confirmLast } })
export const deleteNode = (id, confirmName) => apiFetch(`/servers/${id}`, { method: 'DELETE', body: { confirm_name: confirmName } })
export const updateNodeRouting = (id, payload) => apiFetch(`/servers/${id}/routing`, { method: 'PUT', body: payload })
export const fetchJob = (id) => apiFetch(`/provision-jobs/${id}`)
export const fetchNodeJobs = (serverId) => apiFetch(`/provision-jobs?server_id=${serverId}`)
export const resumeJob = (id) => apiFetch(`/provision-jobs/${id}/resume`, { method: 'POST' })
export const fetchHubKey = () => apiFetch('/ssh-keys/hub')
// «Серверы» (B1/B5/B6): id 'hub' = the Hub.
export const fetchHub = () => apiFetch('/hub')
// «Адрес хаба»: the wizard that moves the Hub to an additional address (servers.read to look,
// hub.address to act).
export const fetchHubAddresses = () => apiFetch('/hub/addresses')
export const startHubAddressChange = (ip) => apiFetch('/hub/address-changes', { method: 'POST', body: { ip } })
export const fetchHubAddressChange = (id) => apiFetch(`/hub/address-changes/${id}`)
export const runHubAddressStep = (id, step) => apiFetch(`/hub/address-changes/${id}/steps/${step}`, { method: 'POST' })
export const hubAddressAction = (id, action, body = {}) => apiFetch(`/hub/address-changes/${id}/${action}`, { method: 'POST', body })
export const fetchHubAddressPhone = (id) => apiFetch(`/hub/address-changes/${id}/phone-check`)
export const fetchServerLoad = (id, range = '24h') => apiFetch(`/servers/${id}/load?range=${encodeURIComponent(range)}`)
export const saveServerOwner = (id, body) => apiFetch(`/servers/${id}/capacity`, { method: 'PUT', body })
export const fetchLaunchChecklist = () => apiFetch('/launch-checklist')
// «Переустановить Telegram-прокси»: a job of its own → 202 {job_id, kind: 'mtproxy'} (nodes.provision).
export const reinstallMTProxy = (id) => apiFetch(`/servers/${id}/mtproxy`, { method: 'POST' })
// Read-only: generate_204 through the exit's Hysteria and TCP paths (nothing is changed).
export function checkServerTunnel(id) {
  return apiFetch(`/servers/${id}/check-tunnel`, { method: 'POST' })
}
export function fetchServerLogs(serverId, { service = 'node-agent', tail = 100 } = {}) {
  const params = new URLSearchParams({ service, tail })
  return apiFetch(`/servers/${serverId}/logs?${params}`).then(data => data.logs || data.error || '')
}

// ── Admin Users & Roles ──
export function fetchAdminUsers() {
  return apiFetch('/admin-users')
}
export function createAdminUser(payload) {
  return apiFetch('/admin-users', { method: 'POST', body: payload })
}
export function updateAdminUser(id, payload) {
  return apiFetch(`/admin-users/${id}`, { method: 'PATCH', body: payload })
}
export function fetchAdminRoles() {
  return apiFetch('/admin-roles')
}
export function createAdminRole(payload) {
  return apiFetch('/admin-roles', { method: 'POST', body: payload })
}
export function updateAdminRole(id, payload) {
  return apiFetch(`/admin-roles/${id}`, { method: 'PATCH', body: payload })
}
export function fetchAdminPermissions() {
  return apiFetch('/admin-permissions')
}

// ── Tenants ──
export function fetchTenantsPaged(params) {
  return pagedFetch('/tenants', params)
}
export function createTenant(payload) {
  return apiFetch('/tenants', { method: 'POST', body: payload })
}
export function updateTenant(id, payload) {
  return apiFetch(`/tenants/${id}`, { method: 'PATCH', body: payload })
}
export function deleteTenant(id) {
  return apiFetch(`/tenants/${id}`, { method: 'DELETE' })
}

// ── Audit Log ──
export function fetchAuditLog(params) {
  return pagedFetch('/audit-log', params)
}

export function fetchTrafficChart(days = 30) {
  return apiFetch(`/traffic-chart?days=${days}`)
}

// ── Settings ──
export function getSettings() {
  return apiFetch('/settings')
}
export function updateSetting(key, value) {
  return apiFetch('/settings', { method: 'POST', body: { key, value } })
}

// ── Support (Telegram bot plan, TG5) ──
export function fetchSupportTickets({ status = 'needs_answer', q = '' } = {}) {
  const p = new URLSearchParams({ status, limit: '100' })
  if (q) p.set('q', q)
  return apiFetch(`/support/tickets?${p}`)
}
export function fetchSupportTicket(id) {
  return apiFetch(`/support/tickets/${id}`)
}
export function markSupportRead(id) {
  return apiFetch(`/support/tickets/${id}/read`, { method: 'POST' })
}
export function sendSupportAnswer(id, body) {
  return apiFetch(`/support/tickets/${id}/messages`, { method: 'POST', body: { body } })
}
export function closeSupportTicket(id) {
  return apiFetch(`/support/tickets/${id}/close`, { method: 'POST' })
}
export function reopenSupportTicket(id) {
  return apiFetch(`/support/tickets/${id}/reopen`, { method: 'POST' })
}
// authImage loads an image that needs the admin session and returns an object URL.
export async function authImage(path) {
  const res = await apiFetch(path, { raw: true })
  if (!res.ok) throw new Error(`Ошибка ${res.status}`)
  return URL.createObjectURL(await res.blob())
}

// ── Telegram users' bot (TG7) and news (TG6) ──
export function fetchTelegramSettings() {
  return apiFetch('/telegram/settings')
}
export function saveTelegramSettings(payload) {
  return apiFetch('/telegram/settings', { method: 'PUT', body: payload })
}
export function checkTelegramBot() {
  return apiFetch('/telegram/check', { method: 'POST' })
}
export function checkTelegramChannel() {
  return apiFetch('/telegram/channel/check', { method: 'POST' })
}
export function fetchStars() {
  return apiFetch('/telegram/stars')
}
export function saveStarsRate(rate) {
  return apiFetch('/telegram/stars', { method: 'PUT', body: { rate } })
}
export function refundStars(id) {
  return apiFetch(`/telegram/stars/${id}/refund`, { method: 'POST' })
}
export function fetchNews() {
  return apiFetch('/news')
}
export function createNews(payload) {
  return apiFetch('/news', { method: 'POST', body: payload })
}
export function updateNews(id, payload) {
  return apiFetch(`/news/${id}`, { method: 'PUT', body: payload })
}
export function deleteNews(id) {
  return apiFetch(`/news/${id}`, { method: 'DELETE' })
}
export function sendNews(id) {
  return apiFetch(`/news/${id}/send`, { method: 'POST' })
}
export function scheduleNews(id, at) {
  return apiFetch(`/news/${id}/schedule`, { method: 'POST', body: { at } })
}
export function unscheduleNews(id) {
  return apiFetch(`/news/${id}/unschedule`, { method: 'POST' })
}
export function uploadNewsImage(file) {
  const form = new FormData()
  form.append('image', file)
  return apiFetch('/news/upload', { method: 'POST', body: form })
}

// ── Alerts (Telegram) ──
export function fetchAlertSettings() {
  return apiFetch('/alerts/settings')
}
export function saveAlertSettings(payload) {
  return apiFetch('/alerts/settings', { method: 'PUT', body: payload })
}
export function sendAlertTest() {
  return apiFetch('/alerts/test', { method: 'POST' })
}
export function fetchAlertEvents(limit = 100) {
  return apiFetch(`/alerts/events?limit=${limit}`)
}

// ── Entries (Hub + reserve entries in RU) ──
export function fetchEntries() {
  return apiFetch('/entries')
}
export function saveEntryAddress(id, payload) {
  return apiFetch(`/entries/${id}`, { method: 'PUT', body: payload })
}
export function saveEntrySettings(patch) {
  return apiFetch('/entries/settings', { method: 'PUT', body: patch })
}
export function fetchEntriesPreview() {
  return apiFetch('/entries/preview')
}
// mode: 'hub' | 'relay' (everyone through target) | 'auto'
export function saveEntryMode(mode, target) {
  return apiFetch('/entries/mode', { method: 'PUT', body: { mode, target: target || 0 } })
}
// The entry panel (docs/prd/entries-management.prd.md): rotation off | test | all (confirm_early: «ДОСРОЧНО»),
// the GRE tunnel on/off (starts «Переустановить»), «Проверить путь» / «Проверить туннель» (jobs).
export const setEntryRotation = (id, { to, reason = '', confirmEarly = '' }) =>
  apiFetch(`/entries/${id}/rotation`, { method: 'PUT', body: { to, reason, confirm_early: confirmEarly } })
export const setEntryTunnel = (id, on) => apiFetch(`/entries/${id}/tunnel`, { method: 'PUT', body: { on } })
export const startEntryCheck = (id, kind) => apiFetch(`/entries/${id}/${kind}`, { method: 'POST' })
// «Проверить с телефона»: start a 30-minute test page on the node / read what reached the phones.
export const startMobileCheck = (serverId) => apiFetch(`/servers/${serverId}/mobile-check`, { method: 'POST' })
export const fetchMobileCheck = (serverId) => apiFetch(`/servers/${serverId}/mobile-check`)

// ── Renewals and the menu's switches (C10) ──
export const fetchFailingRenewals = () => apiFetch('/renewals/failing')
export const fetchFeatures = () => apiFetch('/features')

// ── Subscription sharing (C13) ──
export const fetchUserDialRate = (id) => apiFetch(`/users/${id}/dial-rate`)
export const fetchDialRateSuspects = () => apiFetch('/dial-rate/suspects')

// ── Direct routes ──
export const fetchDirectRoutes = (query) => apiFetch(`/direct-routes${query ? `?${query}` : ''}`)
export const fetchDirectRouteStats = () => apiFetch('/direct-routes/stats')
export const fetchDirectRouteGroups = () => apiFetch('/direct-routes/groups')
export const createDirectRoute = (body) => apiFetch('/direct-routes', { method: 'POST', body })
export const updateDirectRoute = (id, body) => apiFetch(`/direct-routes/${id}`, { method: 'PUT', body })
export const deleteDirectRoute = (id) => apiFetch(`/direct-routes/${id}`, { method: 'DELETE' })
export const bulkDirectRoutes = (action, ids) => apiFetch('/direct-routes/bulk', { method: 'POST', body: { action, ids } })
export const importDirectRoutes = (body) => apiFetch('/direct-routes/import', { method: 'POST', body })
export const discoverDirectRoute = (rootDomain) => apiFetch('/direct-routes/discover', { method: 'POST', body: { root_domain: rootDomain } })

// ── B2B ──
export const fetchB2BAnalytics = () => apiFetch('/b2b-analytics')
export const fetchUnassignedB2BInvoices = () => apiFetch('/payments/unassigned-b2b-invoices')
export const attachPaymentToTenant = (paymentId, tenantId) => apiFetch(`/payments/${paymentId}/attach-tenant`, { method: 'POST', body: { tenant_id: tenantId } })

// ── MTProto (Telegram proxy) ──
export const fetchMTProto = () => apiFetch('/mtproto')
export const saveMTProto = (body) => apiFetch('/mtproto', { method: 'PUT', body })
// Only returns a new secret for the domain; nothing is saved until saveMTProto.
export const generateMTProtoSecret = (frontingDomain) => apiFetch('/mtproto/generate-secret', { method: 'POST', body: { fronting_domain: frontingDomain } })

// ── Telegram WEB proxy (tproxy-server on the Hub) ──
// Every call returns the GET payload (settings + status + last run). The secret is never returned.
// PUT/POST answer 409 {error} while a run is in progress.
export const fetchTGWebProxy = () => apiFetch('/telegram/webproxy')
// Turning on (or a new host while on) starts an install run; turning off starts a disable run.
export const saveTGWebProxy = ({ enabled, host }) => apiFetch('/telegram/webproxy', { method: 'PUT', body: { enabled, host } })
export const installTGWebProxy = () => apiFetch('/telegram/webproxy/install', { method: 'POST' })
// A new secret, then an install run re-applies it: every link handed out before stops working.
export const rotateTGWebProxySecret = () => apiFetch('/telegram/webproxy/rotate-secret', { method: 'POST' })

// ── Plans (subscription tariffs) ──
export function fetchPlans() {
  return apiFetch('/plans')
}
export function createPlan(payload) {
  return apiFetch('/plans', { method: 'POST', body: payload })
}
export function updatePlan(id, payload) {
  return apiFetch(`/plans/${id}`, { method: 'PATCH', body: payload })
}
export function deletePlan(id) {
  return apiFetch(`/plans/${id}`, { method: 'DELETE' })
}

// «Выставить счёт» for a tenant: the admin route links the invoice to this tenant (tenants.write).
export function createTenantInvoice(body) {
  return apiFetch('/b2b/create-invoice', { method: 'POST', body })
}

// «Сбросить пароль»: { temp_password, sessions_closed, email_sent } — the password is shown once.
export function resetUserPassword(userDbId) {
  return apiFetch(`/users/${userDbId}/password/reset`, { method: 'POST' })
}

// ── Wallet (W6) ──
export function fetchUserWallet(userDbId) {
  return apiFetch(`/users/${userDbId}/wallet`)
}
export function adjustUserWallet(userDbId, { direction, amountRub, reason, clientText = '', requestId }) {
  return apiFetch(`/users/${userDbId}/wallet/adjust`, {
    method: 'POST',
    body: { direction, amount_rub: amountRub, reason, client_text: clientText, request_id: requestId },
  })
}
export function fetchWalletStats() {
  return apiFetch('/wallet-stats')
}

// 2FA of the signed-in admin (internal/admin/login_security.go).
export const getTOTPStatus = () => apiFetch('/me/totp')
export const setupTOTP = () => apiFetch('/me/totp/setup', { method: 'POST' })
export const enableTOTP = (code) => apiFetch('/me/totp/enable', { method: 'POST', body: { code } })
export const disableTOTP = (password, code) => apiFetch('/me/totp/disable', { method: 'POST', body: { password, code } })
