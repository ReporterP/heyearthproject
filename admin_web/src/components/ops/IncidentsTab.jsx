import { useEffect, useState } from 'react'
import { useToast } from '../Toast.jsx'
import { downloadIncident, fetchOpsIncidents, saveIncidentNotes } from '../../api/admin.js'
import { Badge, Button, Card, CardBody, CardHeader, Dialog, EmptyState, Skeleton, Textarea } from '../ui/index.js'
import { incidentView } from './incidentsModel.js'

/**
 * IncidentsTab — «Операции → Журнал»: incidents built from the alert journal over 30 days
 * (a critical alert, or a warning longer than 30 minutes; spans closer than 10 minutes merge),
 * the owner's notes, and «Разбор .md» — a draft of the post-mortem for docs/incidents/.
 * Props: canWrite (settings.write: notes).
 */
export default function IncidentsTab({ canWrite }) {
  const toast = useToast()
  const [list, setList] = useState(null)
  const [error, setError] = useState('')
  const [edit, setEdit] = useState(null) // { id, notes }
  const [busy, setBusy] = useState(false)

  async function load() {
    try {
      const r = await fetchOpsIncidents()
      setList(r.incidents || [])
      setError('')
    } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  async function save() {
    setBusy(true)
    try {
      await saveIncidentNotes(edit.id, edit.notes)
      toast.success('Заметка сохранена')
      setEdit(null)
      await load()
    } catch (e) { toast.error(e.message) } finally { setBusy(false) }
  }

  async function exportMd(inc, filename) {
    try { await downloadIncident(inc.id, filename) } catch (e) { toast.error(e.message) }
  }

  if (error && !list) return <EmptyState title="«Журнал» не загрузился" description={error} action={<Button size="sm" variant="secondary" onClick={load}>Повторить</Button>} />
  if (!list) return <Skeleton className="h-64 w-full" />

  const now = Date.now()
  return (
    <Card>
      <CardHeader className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3">
        <span className="text-sm font-medium text-primary">Инциденты за 30 дней</span>
        <span className="text-xs text-tertiary sm:ml-auto">критичная тревога или «внимание» дольше 30 минут; рядом стоящие сливаются</span>
      </CardHeader>
      <CardBody className="p-0">
        {list.length === 0 ? (
          <EmptyState className="py-6" title="Инцидентов не было" description="Здесь появится каждая критичная тревога и долгое «внимание» — с заметками и выгрузкой разбора." />
        ) : (
          <ul className="divide-y divide-default">
            {list.map(inc => {
              const v = incidentView(inc, now)
              return (
                <li key={inc.id} className="px-4 py-3 flex flex-col gap-1">
                  <div className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3">
                    <Badge variant={v.tone}>{v.levelLabel}</Badge>
                    <span className="text-sm text-primary">{inc.title}</span>
                    {v.open && <Badge variant="danger">идёт</Badge>}
                    <span className="text-xs text-secondary sm:ml-auto font-mono tabular-nums">{v.when} · {v.duration}</span>
                  </div>
                  <div className="flex flex-wrap gap-1">
                    {(inc.keys || []).map(k => <span key={k} className="font-mono text-xs text-tertiary">{k}</span>)}
                  </div>
                  {inc.notes && <p className="text-xs text-secondary whitespace-pre-wrap">{inc.notes}</p>}
                  <div className="flex flex-wrap gap-2">
                    {canWrite && <Button size="sm" variant="ghost" onClick={() => setEdit({ id: inc.id, notes: inc.notes || '' })}>{inc.notes ? 'Изменить заметку' : 'Заметка'}</Button>}
                    <Button size="sm" variant="ghost" onClick={() => exportMd(inc, v.filename)}>Разбор .md</Button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </CardBody>
      <Dialog open={!!edit} onClose={() => setEdit(null)} title="Заметка к инциденту">
        {edit && (
          <div className="flex flex-col gap-3">
            <Textarea rows={6} value={edit.notes} onChange={e => setEdit({ ...edit, notes: e.target.value })} aria-label="Заметка" />
            <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2">
              <Button variant="ghost" onClick={() => setEdit(null)}>Отмена</Button>
              <Button variant="primary" loading={busy} onClick={save}>Сохранить</Button>
            </div>
          </div>
        )}
      </Dialog>
    </Card>
  )
}
