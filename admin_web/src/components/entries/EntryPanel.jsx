import { useEffect, useState } from 'react'
import { Activity, Cable, ExternalLink, Smartphone, Trash2 } from 'lucide-react'
import { useToast } from '../Toast.jsx'
import { setEntryRotation, setEntryTunnel, startEntryCheck, deprovisionNode, deleteNode, saveEntryAddress } from '../../api/admin.js'
import { formatDate } from '../../utils/format.js'
import { Badge, Button, Dialog, Input, Select, Table, Thead, Tbody, Tr, Th, Td } from '../ui/index.js'
import { ConfirmDanger } from '../ui/ConfirmDanger.jsx'
import JobProgress from '../nodes/JobProgress.jsx'
import { deprovisionConsequences, deleteConsequences, reprovisionConsequences } from '../nodes/drawerModel.js'
import { entryActions, rotationView, stabilityView } from './entriesModel.js'

const EARLY_WORD = 'ДОСРОЧНО'
// Full class names (Tailwind generates only what it finds in the source).
const TONE_TEXT = { success: 'text-success', warning: 'text-warning', danger: 'text-danger', default: 'text-tertiary', info: 'text-info' }
const JOB_STATUS = {
  succeeded: ['прошла', 'success'], failed: ['не прошла', 'danger'], running: ['идёт', 'info'], queued: ['в очереди', 'info'],
  interrupted: ['прервана', 'warning'],
}

function CheckLine({ title, job, fresh, freshText }) {
  if (!job) return <div className="flex items-center justify-between gap-2 text-sm"><span className="text-secondary">{title}</span><span className="text-xs text-tertiary">не запускалась</span></div>
  const [label, variant] = JOB_STATUS[job.status] || [job.status, 'default']
  return (
    <div className="flex items-center justify-between gap-2 text-sm">
      <span className="text-secondary">{title}</span>
      <span className="flex items-center gap-2 min-w-0">
        <Badge variant={variant}>{label}</Badge>
        {job.finished_at && <span className="font-mono tabular-nums text-xs text-tertiary">{formatDate(job.finished_at)}</span>}
        {fresh && <span className="text-xs text-success">{freshText}</span>}
      </span>
    </div>
  )
}

// Action: a button with the reason it is off right under it (no tooltips: the reason must be readable on a phone).
function Action({ label, icon, variant = 'secondary', act, onClick, loading }) {
  return (
    <div className="min-w-0">
      <Button size="sm" variant={variant} icon={icon} disabled={!act.enabled} loading={loading} onClick={onClick} className="w-full sm:w-auto">{label}</Button>
      {act.reason && <p className="text-[11px] text-tertiary mt-1">{act.reason}</p>}
    </div>
  )
}

/**
 * EntryPanel — one reserve entry of «Входы»: its state and stability by kind, the last checks and every
 * action (docs/prd/entries-management.prd.md): «Проверить путь», out of rotation / back to the canary /
 * to everyone, «Проверить туннель» and the tunnel on/off, the phone check, the address in the subscription,
 * the server card and deletion (the same jobs and confirmations as «Серверы»).
 * Props: entry (a GET /entries row) or null, data (the whole GET /entries answer), node (GET /nodes item),
 * canEdit, onClose, onChanged (reload), onOpenNode(id), onPhone(id).
 */
export default function EntryPanel({ entry, data, node, canEdit, onClose, onChanged, onOpenNode, onPhone }) {
  const toast = useToast()
  const [jobId, setJobId] = useState(null)
  const [busy, setBusy] = useState('')
  const [reason, setReason] = useState('')
  const [asking, setAsking] = useState(null) // toTest | toAll | tunnelOn | tunnelOff | deprovision | record
  useEffect(() => { setJobId(null); setReason(''); setAsking(null) }, [entry?.id])

  if (!entry) return null
  const acts = entryActions(entry, { testUser: data?.failover?.test_user || '', failover: data?.failover, now: new Date() })
  const rot = rotationView(entry)
  const stab = stabilityView(entry.stability)
  const running = [entry.checks?.path, entry.checks?.tunnel, entry.checks?.setup].find((j) => j && (j.status === 'running' || j.status === 'queued'))
  const shownJob = jobId || running?.id || null
  const name = entry.name || `Вход ${entry.id}`

  async function run(key, fn, ok) {
    setBusy(key)
    try {
      const r = await fn()
      if (r?.job_id) setJobId(r.job_id)
      if (ok) toast.success(ok)
      await onChanged()
    } catch (e) { toast.error(e.message) } finally { setBusy('') }
  }
  // confirmed: for ConfirmDanger — a failure throws and stays in the dialog.
  async function confirmed(fn, ok) {
    const r = await fn()
    if (r?.job_id) setJobId(r.job_id)
    toast.success(ok)
    await onChanged()
  }
  const takeOut = () => run('out', () => setEntryRotation(entry.id, { to: 'off', reason }),
    'Вход выведен из ротации — подписки пользователей обновятся без него')

  const confirms = {
    toTest: {
      title: 'Вернуть вход: только тестовый пользователь', actionLabel: 'Вернуть тестовому', confirmText: name,
      consequences: [
        `Вход получит только «Тестовый пользователь» (${data?.failover?.test_user || '—'}): строкой «· резерв» и путём в «⚡ Авто».`,
        'Проверьте его сутки на своём телефоне — в мобильной сети и в Wi-Fi, — затем «Всем».',
      ],
      onConfirm: () => confirmed(() => setEntryRotation(entry.id, { to: 'test' }), 'Вход у тестового пользователя'),
    },
    toAll: {
      title: 'Вернуть вход всем пользователям', actionLabel: 'Всем', confirmText: acts.toAll.early ? EARLY_WORD : name,
      consequences: [
        'Вход появится в подписке у всех пользователей.',
        'Сутки все приложения будут обновлять подписку раз в час (срочное обновление): если что-то пойдёт не так, вывод дойдёт до всех за час.',
        ...(acts.toAll.early ? [`Вход на тесте меньше суток — правило канарейки нарушается. Чтобы всё равно включить, введите «${EARLY_WORD}».`] : []),
      ],
      onConfirm: () => confirmed(() => setEntryRotation(entry.id, { to: 'all', confirmEarly: acts.toAll.early ? EARLY_WORD : '' }), 'Вход возвращён всем'),
    },
    tunnelOn: {
      title: 'Включить туннель до Hub', actionLabel: 'Включить', confirmText: name,
      consequences: [
        'Начнётся задача «Включение туннеля» — переустановка входа с туннелем: сначала поднимется туннель, затем пересылка и nginx входа перейдут в него. Выбор сохранится, только если задача пройдёт до конца.',
        ...reprovisionConsequences(),
      ],
      onConfirm: () => confirmed(() => setEntryTunnel(entry.id, true), 'Туннель включается — следите за задачей'),
    },
    tunnelOff: {
      title: 'Выключить туннель до Hub', actionLabel: 'Выключить', confirmText: name,
      consequences: [
        'Начнётся задача «Выключение туннеля»: пересылка и nginx входа перейдут на публичный адрес Hub, затем оба конца туннеля снимутся. Выбор сохранится, только если задача пройдёт до конца.',
        'Если у провайдера входа теряются потоки до Hub, без туннеля это снова дойдёт до пользователей.',
        ...reprovisionConsequences(),
      ],
      onConfirm: () => confirmed(() => setEntryTunnel(entry.id, false), 'Туннель выключается — следите за задачей'),
    },
    deprovision: {
      title: 'Удалить вход: отключить сервер', actionLabel: 'Отключить', confirmText: name,
      consequences: deprovisionConsequences(node || { role: 'relay', lifecycle_state: entry.lifecycle }),
      onConfirm: () => confirmed(() => deprovisionNode(entry.id, name), 'Вход отключается — после задачи удалите запись'),
    },
    record: {
      title: 'Удалить запись входа', actionLabel: 'Удалить запись', confirmText: name,
      consequences: deleteConsequences(),
      onConfirm: async () => { await deleteNode(entry.id, name); toast.success('Запись входа удалена'); await onChanged(); onClose() },
    },
  }
  const ask = asking && confirms[asking]

  return (
    <Dialog open={!!entry} onClose={onClose} title={`${name} — ${entry.address || '—'}`} maxWidth="max-w-3xl">
      <div className="space-y-5 text-sm">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={rot.tone}>{rot.label}</Badge>
          {rot.since && <span className="font-mono tabular-nums text-xs text-tertiary">с {formatDate(rot.since)}</span>}
          <Badge variant={entry.tunnel ? 'accent' : 'default'}>{entry.tunnel ? 'туннель GRE' : 'без туннеля'}</Badge>
          {rot.reason && <span className="text-xs text-secondary min-w-0 break-words">{rot.reason}</span>}
        </div>

        <section className="space-y-2">
          <h4 className="text-xs font-medium text-tertiary">Стабильность: доля успешных подключений через выход</h4>
          {stab.byKind.length === 0 ? <p className="text-xs text-tertiary">Данных пока нет: монитор пишет их раз в минуту.</p> : (
            <Table>
              <Thead><Tr><Th>Путь</Th><Th align="right">1 ч</Th><Th align="right">24 ч</Th></Tr></Thead>
              <Tbody>
                {stab.byKind.map((k) => (
                  <Tr key={k.kind}>
                    <Td mono>{k.label}</Td>
                    <Td mono align="right"><span className={TONE_TEXT[k.h1.tone]}>{k.h1.few ? 'мало данных' : k.h1.text}</span></Td>
                    <Td mono align="right"><span className={TONE_TEXT[k.h24.tone]}>{k.h24.few ? 'мало данных' : k.h24.text}</span></Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          )}
        </section>

        <section className="space-y-1.5">
          <h4 className="text-xs font-medium text-tertiary">Проверки</h4>
          <CheckLine title="Проверить путь" job={entry.checks?.path} fresh={entry.checks?.path_fresh} freshText="годится для возврата" />
          <CheckLine title="Проверить туннель" job={entry.checks?.tunnel} fresh={entry.checks?.tunnel_fresh} freshText="туннель можно включать" />
          <CheckLine title="Последняя настройка" job={entry.checks?.setup} />
        </section>

        {shownJob && <JobProgress jobId={shownJob} canProvision={canEdit} tcpEnabled={false} onFinished={() => onChanged()} />}

        {canEdit && (
          <section className="space-y-3 border-t border-default pt-4">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <Action label="Проверить путь" icon={<Activity className="w-3.5 h-3.5" />} act={acts.pathcheck} loading={busy === 'path'}
                onClick={() => run('path', () => startEntryCheck(entry.id, 'pathcheck'), 'Проверка пути запущена: 3–6 минут')} />
              {entry.rotation === 'off'
                ? <Action label="Вернуть: только тест" act={acts.toTest} onClick={() => setAsking('toTest')} />
                : entry.rotation === 'test'
                  ? <Action label="Всем" variant="primary" act={acts.toAll} onClick={() => setAsking('toAll')} />
                  : <span />}
            </div>
            {entry.rotation !== 'off' && (
              <div className="flex flex-col sm:flex-row sm:items-end gap-2">
                <div className="w-full sm:flex-1">
                  <label htmlFor={`entry-reason-${entry.id}`} className="text-xs text-tertiary block mb-1">Причина вывода (видна на «Входах» и в журнале)</label>
                  <Input id={`entry-reason-${entry.id}`} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="например: потери у провайдера" maxLength={200} />
                </div>
                <Button variant="danger" size="sm" disabled={!acts.out.enabled || reason.trim().length < 3} loading={busy === 'out'} onClick={takeOut}>Вывести из ротации</Button>
              </div>
            )}
            {entry.rotation !== 'off' && acts.out.reason && <p className="text-[11px] text-tertiary">{acts.out.reason}</p>}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              {entry.tunnel
                ? <Action label="Выключить туннель" icon={<Cable className="w-3.5 h-3.5" />} act={acts.tunnelOff} onClick={() => setAsking('tunnelOff')} />
                : <>
                    <Action label="Проверить туннель" icon={<Cable className="w-3.5 h-3.5" />} act={acts.tunnelcheck} loading={busy === 'tcheck'}
                      onClick={() => run('tcheck', () => startEntryCheck(entry.id, 'tunnelcheck'), 'Проверка туннеля запущена: пользователи её не заметят')} />
                    <Action label="Включить туннель" act={acts.tunnelOn} onClick={() => setAsking('tunnelOn')} />
                  </>}
            </div>
            <div className="flex flex-col sm:flex-row sm:flex-wrap gap-2">
              <Button size="sm" variant="secondary" icon={<Smartphone className="w-3.5 h-3.5" />} onClick={() => onPhone?.(entry.id)}>Проверить с телефона</Button>
              {onOpenNode && <Button size="sm" variant="ghost" icon={<ExternalLink className="w-3.5 h-3.5" />} onClick={() => onOpenNode(entry.id)}>Карточка сервера</Button>}
              <Select aria-label="Адрес входа в подписке" value={entry.address_mode} className="h-8 text-xs w-full sm:w-auto"
                onChange={(ev) => run('addr', () => saveEntryAddress(entry.id, { address_mode: ev.target.value }), 'Адрес входа сохранён')}>
                <option value="ip">В подписке: IP-адрес</option>
                <option value="domain" disabled={!data?.domain_mode_available}>В подписке: домен{data?.domain_mode_available ? '' : ' — после раздела «Домены»'}</option>
              </Select>
            </div>
            <div className="border-t border-default pt-3">
              <Action label={acts.remove.stage === 'record' ? 'Удалить запись входа' : 'Удалить вход'} variant="danger" icon={<Trash2 className="w-3.5 h-3.5" />}
                act={acts.remove} onClick={() => setAsking(acts.remove.stage === 'record' ? 'record' : 'deprovision')} />
              <p className="text-[11px] text-tertiary mt-1">Сначала сервер отключается (пересылка снимается), затем удаляется запись. Сам сервер у хостера не удаляется.</p>
            </div>
          </section>
        )}
      </div>
      {ask && (
        <ConfirmDanger open onClose={() => setAsking(null)} title={ask.title} consequences={ask.consequences}
          confirmText={ask.confirmText} actionLabel={ask.actionLabel} onConfirm={ask.onConfirm} />
      )}
    </Dialog>
  )
}
