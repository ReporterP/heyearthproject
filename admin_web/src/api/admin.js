import { apiFetch, pagedFetch } from './client.js'

// ── Auth ──
export function login(username, password) {
  return apiFetch('/login', { method: 'POST', body: { username, password } })
}

// ── Dashboard ──
export function fetchOverview() {
  return apiFetch('/overview')
}

// ── Users ──
export function fetchUsersPaged(params) {
  return pagedFetch('/users', params)
}
export function createUser(payload) {
  return apiFetch('/users', { method: 'POST', body: payload })
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

// ── Groups ──
export function fetchGroupsPaged(params) {
  return pagedFetch('/groups', params)
}
export function fetchGroups(pageSize = 200) {
  return pagedFetch('/groups', { pageSize })
}
export function createGroup(payload) {
  return apiFetch('/groups', { method: 'POST', body: payload })
}
export function updateGroup(id, payload) {
  return apiFetch(`/groups/${id}`, { method: 'PATCH', body: payload })
}
export function deleteGroup(id) {
  return apiFetch(`/groups/${id}`, { method: 'DELETE' })
}

// ── Subscriptions ──
export function fetchSubscriptionsPaged(params) {
  return pagedFetch('/subscriptions', params)
}
export function saveSubscription(payload) {
  return apiFetch('/subscriptions', { method: 'POST', body: payload })
}
export function deleteSubscription(userDbId, groupId) {
  return apiFetch(`/subscriptions/${userDbId}/${groupId}`, { method: 'DELETE' })
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

// ── Servers ──
export function fetchServersPaged(params) {
  return pagedFetch('/servers', params)
}
export function fetchServerDetail(id) {
  return apiFetch(`/servers/${id}`)
}
export function createServer(payload) {
  return apiFetch('/servers', { method: 'POST', body: payload })
}
export function updateServer(id, payload) {
  return apiFetch(`/servers/${id}`, { method: 'PATCH', body: payload })
}
export function deleteServer(id) {
  return apiFetch(`/servers/${id}`, { method: 'DELETE' })
}
export function runServerAction(id, action) {
  return apiFetch(`/servers/${id}/actions`, { method: 'POST', body: { action } })
}
export function fetchServerLogs(serverId, { service = 'node-agent', tail = 100 } = {}) {
  const params = new URLSearchParams({ service, tail })
  return apiFetch(`/servers/${serverId}/logs?${params}`, { raw: true }).then(r => r.text())
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

// ── Settings ──
export function getSettings() {
  return apiFetch('/settings')
}
export function updateSetting(key, value) {
  return apiFetch('/settings', { method: 'POST', body: { key, value } })
}
