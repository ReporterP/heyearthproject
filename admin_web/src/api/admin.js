import { apiFetch, pagedFetch } from './client.js'

// ── Auth ──
export function login(username, password) {
  // A rejected login (401/403) shows the server's reason and must not run the logout path.
  return apiFetch('/login', { method: 'POST', body: { username, password }, sessionErrors: false })
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
export function fetchUserDevices(userId) {
  return apiFetch(`/users/${userId}/devices`)
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

// ── Subscriptions ──
export function fetchSubscriptionsPaged(params) {
  return pagedFetch('/subscriptions', params)
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
export function resendPaymentNotifications() {
  return apiFetch('/payments/resend', { method: 'POST' })
}

// ── Servers ──
export function fetchServersPaged(params) {
  return pagedFetch('/servers', params)
}
export function fetchServerDetail(id) {
  return apiFetch(`/servers/${id}`)
}
export function fetchHysteria2Status() {
  return apiFetch('/hysteria2/status')
}
export function updateServer(id, payload) {
  return apiFetch(`/servers/${id}`, { method: 'PATCH', body: payload })
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
export function fetchServerFiles(serverId, path) {
  const params = new URLSearchParams({ path })
  return apiFetch(`/servers/${serverId}/files?${params}`)
}

// ── SSH Keys ──
export function fetchSshKeys() {
  return apiFetch('/ssh-keys')
}
export function createSshKey(name, privateKey) {
  return apiFetch('/ssh-keys', { method: 'POST', body: { name, private_key: privateKey } })
}
export function updateSshKey(id, name, privateKey) {
  return apiFetch(`/ssh-keys/${id}`, { method: 'PATCH', body: { name, private_key: privateKey } })
}
export function deleteSshKey(id) {
  return apiFetch(`/ssh-keys/${id}`, { method: 'DELETE' })
}

// ── Releases ──
export function fetchReleases() {
  return apiFetch('/releases')
}
export function uploadRelease({ platform, version, releaseNotes, mandatory, minSupportedVersion, file, activate }) {
  const fd = new FormData()
  fd.append('platform', platform)
  fd.append('version', version)
  if (releaseNotes) fd.append('release_notes', releaseNotes)
  fd.append('mandatory', mandatory ? 'true' : 'false')
  if (minSupportedVersion) fd.append('min_supported_version', minSupportedVersion)
  fd.append('activate', activate ? 'true' : 'false')
  fd.append('file', file)
  return apiFetch('/releases', { method: 'POST', body: fd })
}
export function activateRelease(id) {
  return apiFetch('/releases/activate', { method: 'POST', body: { release_id: id } })
}

// ── Traffic Masking ──
export function fetchTrafficMaskingRules() {
  return apiFetch('/traffic-masking')
}
export function createTrafficMaskingRule(payload) {
  return apiFetch('/traffic-masking', { method: 'POST', body: payload })
}
export function updateTrafficMaskingRule(id, payload) {
  return apiFetch(`/traffic-masking/${id}`, { method: 'PATCH', body: payload })
}
export function deleteTrafficMaskingRule(id) {
  return apiFetch(`/traffic-masking/${id}`, { method: 'DELETE' })
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

// ── Traffic ──
export function fetchTrafficUsers(params) {
  return pagedFetch('/traffic', params)
}
export function fetchTrafficUserDetail(userId, period = 'month') {
  return apiFetch(`/traffic/${encodeURIComponent(userId)}?period=${period}`)
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
// «Проверить с телефона»: start a 30-minute test page on the node / read what reached the phones.
export const startMobileCheck = (serverId) => apiFetch(`/servers/${serverId}/mobile-check`, { method: 'POST' })
export const fetchMobileCheck = (serverId) => apiFetch(`/servers/${serverId}/mobile-check`)

// ── Auto-Tuner ──
export function fetchAutoTunerStatus() {
  return apiFetch('/auto-tuner/status')
}
export function fetchAutoTunerHistory(page = 1, pageSize = 30) {
  return apiFetch(`/auto-tuner/history?page=${page}&page_size=${pageSize}`)
}
export function toggleAutoTuner(enabled) {
  return apiFetch('/auto-tuner/toggle', { method: 'POST', body: { enabled } })
}
export function setAutoTunerInterval(hours) {
  return apiFetch('/auto-tuner/interval', { method: 'POST', body: { hours } })
}
export function runAutoTunerNow() {
  return apiFetch('/auto-tuner/run-now', { method: 'POST' })
}
export function cleanupAutoTunerHistory(days = 7) {
  return apiFetch('/auto-tuner/cleanup', { method: 'POST', body: { days } })
}

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

// ── Wallet (W6) ──
export function fetchUserWallet(userDbId) {
  return apiFetch(`/users/${userDbId}/wallet`)
}
export function adjustUserWallet(userDbId, { direction, amountRub, reason, requestId }) {
  return apiFetch(`/users/${userDbId}/wallet/adjust`, {
    method: 'POST',
    body: { direction, amount_rub: amountRub, reason, request_id: requestId },
  })
}
export function fetchWalletStats() {
  return apiFetch('/wallet-stats')
}
