import { Badge } from './ui/index.js'
import { lifecycleView } from '../utils/serverState.js'

export default function ServerLifecycleBadge({ state }) {
  const v = lifecycleView(state)
  return <Badge variant={v.variant}>{v.label}</Badge>
}
