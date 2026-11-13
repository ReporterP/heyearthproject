import { Navigate, useLocation, useParams } from 'react-router-dom'
import { serversHref } from '../components/servers/serversModel.js'

// Old addresses of «Ноды», «Входы» and the server page land on «Серверы» (bookmarks, alert links).

// /nodes?role=relay&node=7&add=1 → /servers?tab=relay&node=7&add=1
export function NodesRedirect() {
  const q = new URLSearchParams(useLocation().search)
  return <Navigate replace to={serversHref({ role: q.get('role'), node: q.get('node'), add: q.get('add') === '1' })} />
}

// /entries → the «Входы в РФ» tab
export function EntriesRedirect() {
  return <Navigate replace to={serversHref({ tab: 'relay' })} />
}

// /servers/:id → the card (the page finds the node's tab itself)
export function ServerIdRedirect() {
  const { id } = useParams()
  return <Navigate replace to={serversHref({ node: id })} />
}
