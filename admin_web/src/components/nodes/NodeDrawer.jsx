// Placeholder for the node card — Task 2.15 replaces this file entirely (tabs, jobs, key
// confirmation, routing, danger zone).
import { Link } from 'react-router-dom'
import { Badge, Drawer } from '../ui/index.js'
import { nodeState, keyTypeLabel } from './nodeModel.js'

export default function NodeDrawer({ node, onClose }) {
  const st = nodeState(node)
  return (
    <Drawer open onClose={onClose} title={node.name} subtitle={`${node.country_code} · ${node.ip_address}`}>
      <div className="p-4 space-y-3 text-sm">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={st.variant}>{st.label}</Badge>
          {st.hint && <span className="text-xs text-tertiary">{st.hint}</span>}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs text-tertiary">Отпечаток ключа{node.host_key?.key_type ? ` (${keyTypeLabel(node.host_key.key_type)})` : ''}</span>
          <code className="font-mono text-xs text-primary break-all">{node.host_key?.fingerprint || '—'}</code>
          <Badge variant={node.host_key?.confirmed ? 'success' : 'warning'}>{node.host_key?.confirmed ? 'подтверждён' : 'не подтверждён'}</Badge>
        </div>
        <p className="text-xs text-tertiary">Подтверждение отпечатка, задачи, тарифы и приоритет появятся в карточке ноды в следующем обновлении админки.</p>
        <Link to={`/servers/${node.id}`} className="inline-block text-sm text-accent hover:underline">Метрики и журналы</Link>
      </div>
    </Drawer>
  )
}
