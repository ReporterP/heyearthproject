const API_BASE = import.meta.env.VITE_API_BASE || ''

export async function createTenant({ email, companyName, specs }) {
  const resp = await fetch(`${API_BASE}/api/public/create-tenant`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      company_name: companyName,
      email,
      max_users: specs.users || 10,
      max_servers: specs.ownServers || 5,
      use_our_servers: !!specs.useProviderServers,
    }),
  })
  if (!resp.ok) {
    const data = await resp.json().catch(() => null)
    throw new Error(data?.error || 'Ошибка создания аккаунта')
  }
  return resp.json()
}
