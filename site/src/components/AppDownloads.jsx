import Icon from './Icon.jsx'
import { HAPP } from '../lib/apps.js'

// Happ download rows (spec 02 §33 `dl`): the whole row is a link to the store.
export default function AppDownloads({ only }) {
  const rows = only ? [HAPP[only]] : [HAPP.ios, HAPP.android, HAPP.desktop]
  return (
    <div className="stack gap-3">
      {rows.map((a) => (
        <a key={a.url} href={a.url} className="dl" target="_blank" rel="noopener noreferrer">
          <span className="dl__i" aria-hidden="true"><Icon name={a.icon} size={20} /></span>
          <span><b>{a.label}</b><br /><span className="t-sm subtle">{a.store}</span><span className="sr"> — скачать (откроется в новой вкладке)</span></span>
          <span className="btn btn--secondary btn--sm" aria-hidden="true">Скачать</span>
        </a>
      ))}
    </div>
  )
}
