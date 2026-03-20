import { useEffect, useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { fetchOverview } from '../api/admin.js'
import StatCard from '../components/StatCard.jsx'

export default function DashboardPage() {
  const { t } = useT()
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    fetchOverview()
      .then(setData)
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <p className="text-text-secondary">{t.loading}</p>
  if (error) return <p className="text-red-400">{error}</p>

  const cards = [
    { label: t.dashUsers, value: data?.users_count, sub: `${data?.active_users_count ?? 0} ${t.dashActiveUsers}` },
    { label: t.dashServers, value: data?.servers_count, sub: `${data?.online_servers_count ?? 0} ${t.dashOnlineServers}` },
    { label: t.dashGroups, value: data?.groups_count },
    { label: t.dashPromocodes, value: data?.promocodes_count },
    { label: t.dashPayments, value: data?.payments_count },
    { label: t.dashActiveReleases, value: data?.active_releases },
    { label: t.dashAdmins, value: data?.admins_count },
  ]

  return (
    <div>
      <h2 className="text-2xl font-bold mb-6">{t.dashboardTitle}</h2>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
        {cards.map(c => (
          <StatCard key={c.label} label={c.label} value={c.value} sub={c.sub} />
        ))}
      </div>
    </div>
  )
}
