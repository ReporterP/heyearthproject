import { ShieldCheck } from 'lucide-react'
import HubKeyBox from '../entries/HubKeyBox.jsx'
import { PREP_PORTS, NO_PASSWORD_NOTE } from './serversModel.js'

/**
 * PrepareServer — «Подготовьте сервер у хостера» (the wizard's first screen, B2): the Hub key to
 * paste when creating the server and the ports to open in the hoster's firewall for the role.
 * The Hub checks the TCP ones itself before installing anything.
 */
export default function PrepareServer({ role }) {
  const ports = PREP_PORTS[role === 'relay' ? 'relay' : 'exit']
  return (
    <div className="space-y-3">
      <HubKeyBox />
      <div className="rounded border border-default bg-elevated p-3">
        <div className="flex items-center gap-2 text-sm text-primary mb-1">
          <ShieldCheck className="w-4 h-4 text-accent" aria-hidden="true" />
          Откройте порты в панели хостера
        </div>
        <p className="text-xs text-tertiary mb-2">
          Если у хостера есть файрвол (группа безопасности), разрешите входящие подключения. Без файрвола ничего делать не нужно —
          перед установкой Hub сам проверит, доходят ли до сервера TCP-порты.
        </p>
        <ul className="divide-y divide-[var(--color-border-default)]">
          {ports.map((p) => (
            <li key={`${p.proto}-${p.port}`} className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3 py-1.5">
              <span className="font-mono tabular-nums text-sm text-primary sm:w-40 shrink-0">{p.proto} {p.port}</span>
              <span className="text-xs text-secondary">{p.note}</span>
            </li>
          ))}
        </ul>
      </div>
      <p className="text-xs text-secondary">{NO_PASSWORD_NOTE}</p>
    </div>
  )
}
