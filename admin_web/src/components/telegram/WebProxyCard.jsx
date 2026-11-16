import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { ChevronRight, Download, KeyRound, RefreshCw } from 'lucide-react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { fetchTGWebProxy, saveTGWebProxy, installTGWebProxy, rotateTGWebProxySecret } from '../../api/admin.js'
import { requestSequencer } from '../../utils/polling.js'
import { Badge, Button, Card, CardBody, CardHeader, Dialog, Input, Skeleton, Spinner, Switch } from '../ui/index.js'
import {
  RUN_POLL_MS, IDLE_POLL_MS, isRunning, overallState, formFrom, payloadFrom, effectiveHost, hostError, saveCheck, saveMessage,
  fmtInt, humanBytes, fmtDateTime, agoText, probeView, servicesList, disconnectsView, certView, dnsView, runView, stepView,
  stepKey, autoOpenStep,
} from './webProxyModel.js'

const TONE = { default: 'text-primary', success: 'text-success', warning: 'text-warning', danger: 'text-danger' }

function SectionTitle({ children }) {
  return <h3 className="text-[11px] uppercase tracking-wider text-tertiary">{children}</h3>
}

// Row: one dense line of «Состояние» — label left, value right (wraps under the label on narrow screens).
function Row({ label, children, tone = 'default', mono = false, title }) {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 min-h-9 py-1.5 border-b border-default last:border-b-0">
      <span className="text-xs text-secondary">{label}</span>
      <span title={title} className={`ml-auto min-w-0 text-right text-sm break-words ${TONE[tone] || TONE.default} ${mono ? 'font-mono tabular-nums' : ''}`}>
        {children}
      </span>
    </div>
  )
}

function Box({ tone, children }) {
  const cls = tone === 'danger' ? 'border-danger/30 bg-danger/10 text-danger' : 'border-warning/30 bg-warning/10 text-warning'
  return <div role={tone === 'danger' ? 'alert' : undefined} className={`rounded border px-3 py-2 text-xs break-words ${cls}`}>{children}</div>
}

// StepOutput keeps the newest lines of a running step in view.
function StepOutput({ id, text }) {
  const ref = useRef(null)
  useEffect(() => { if (ref.current) ref.current.scrollTop = ref.current.scrollHeight }, [text])
  if (!text) return <p id={id} className="px-3 pb-2 pl-9 text-xs text-tertiary">Вывода нет.</p>
  return (
    <pre id={id} ref={ref}
      className="mx-3 mb-3 p-2 max-h-64 overflow-y-auto rounded border border-default bg-canvas whitespace-pre-wrap break-words font-mono text-xs text-secondary">
      {text}
    </pre>
  )
}

function StepList({ steps, open, onToggle }) {
  const uid = useId()
  if (!steps?.length) return <p className="text-xs text-tertiary">Шагов пока нет.</p>
  return (
    <ol className="border border-default rounded">
      {steps.map((s, i) => {
        const key = stepKey(s, i)
        const v = stepView(s.status)
        const expanded = open === key
        const outId = `${uid}-${i}`
        return (
          <li key={key} className="border-b border-default last:border-b-0">
            <button type="button" aria-expanded={expanded} aria-controls={expanded ? outId : undefined} onClick={() => onToggle(key)}
              className="w-full flex items-center gap-2 min-h-9 px-3 py-1.5 text-left hover:bg-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent">
              <ChevronRight aria-hidden="true" className={`w-3.5 h-3.5 shrink-0 text-tertiary transition-transform ${expanded ? 'rotate-90' : ''}`} />
              <span className="flex-1 min-w-0 text-sm text-primary break-words">{s.title || s.name || `Шаг ${i + 1}`}</span>
              <Badge variant={v.variant} className="shrink-0">
                {s.status === 'running' && <Spinner size="xs" />}
                {v.label}
              </Badge>
            </button>
            {expanded && <StepOutput id={outId} text={s.output} />}
          </li>
        )
      })}
    </ol>
  )
}

function ConfirmBody({ text, actionLabel, busy, onCancel, onConfirm }) {
  return (
    <div className="space-y-3 text-sm">
      <p className="text-secondary">{text}</p>
      <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2">
        <Button variant="ghost" onClick={onCancel} disabled={busy}>Отмена</Button>
        <Button variant="danger" loading={busy} onClick={onConfirm}>{actionLabel}</Button>
      </div>
    </div>
  )
}

// StatusSection: settings facts + the last check of the relay (status may be null before the first check).
function StatusSection({ data, error, refreshing, onRefresh }) {
  const st = data.status
  const m = st?.metrics || null
  const host = effectiveHost(data.host, data.default_host)
  const liveDiffers = !!data.live_host && data.live_host !== host
  const cert = certView(st?.cert)
  const dns = dnsView(st?.dns)
  const disc = disconnectsView(st?.middle_end_disconnects_5m)
  const healthz = probeView(st?.healthz)
  const readyz = probeView(st?.readyz)
  return (
    <section className="space-y-2" aria-label="Состояние">
      <div className="flex items-center gap-2">
        <SectionTitle>Состояние</SectionTitle>
        {st?.checked_at && (
          <span className="text-xs text-tertiary" title={fmtDateTime(st.checked_at)}>проверено {agoText(st.checked_at)}</span>
        )}
        <Button variant="ghost" size="sm" className="ml-auto" aria-label="Обновить состояние" title="Обновить" onClick={onRefresh}
          disabled={refreshing} icon={<RefreshCw aria-hidden="true" className={`w-3.5 h-3.5 ${refreshing ? 'animate-spin' : ''}`} />} />
      </div>
      {error && <p role="alert" className="text-xs text-danger break-words">Не удалось обновить: {error}</p>}

      <div>
        <Row label="Адрес в работе" mono tone={liveDiffers ? 'warning' : 'default'}
          title={liveDiffers ? `Новый адрес ${host} заработает после установки` : undefined}>
          {data.live_host || '—'}
        </Row>
        <Row label="Секрет" tone={data.secret_set ? 'default' : (data.enabled ? 'warning' : 'default')}>
          {data.secret_set ? 'задан' : 'не задан'}
        </Row>
        <Row label="Сборка tproxy-server" mono={!!data.artifact?.present} tone={data.artifact && !data.artifact.present ? 'warning' : 'default'}>
          {data.artifact ? (data.artifact.present ? (data.artifact.commit || 'есть') : 'нет') : '—'}
        </Row>
        {!st ? (
          <p className="py-2 text-xs text-tertiary">Проверки ещё не было — она появится после установки.</p>
        ) : (
          <>
            <Row label="Relay: healthz" tone={healthz.tone}>{healthz.text}</Row>
            <Row label="Relay: readyz" tone={readyz.tone}>{readyz.text}</Row>
            {st.readyz_error && <p className="py-1 text-xs font-mono text-danger break-all">{st.readyz_error}</p>}
            {servicesList(st.services, data.enabled).map((s) => (
              <Row key={s.unit} label={<span className="font-mono">{s.unit}</span>} tone={s.tone} mono>{s.text}</Row>
            ))}
            <Row label="Сессии / потоки сейчас" mono>{fmtInt(m?.sessions_live)} / {fmtInt(m?.streams_live)}</Row>
            <Row label="Сессий создано всего" mono>{fmtInt(m?.sessions_created_total)}</Row>
            <Row label="Трафик ↑ / ↓" mono title="от клиентов / к клиентам">{humanBytes(m?.bytes_up_total)} / {humanBytes(m?.bytes_down_total)}</Row>
            <Row label="MTProxy: разрывов middle-end за 5 мин" mono tone={disc.tone}>{disc.text}</Row>
            <Row label="Сертификат" tone={cert.tone} mono={cert.text !== 'нет' && cert.text !== '—'}
              title={st.cert?.not_after ? `до ${fmtDateTime(st.cert.not_after)}` : undefined}>
              {cert.text}
            </Row>
            {cert.detail && <p className="py-1 text-xs font-mono text-warning break-all">{cert.detail}</p>}
            {dns && (
              <>
                <Row label="DNS: A-записи" mono tone={dns.ok ? 'default' : 'warning'}>{dns.a.length ? dns.a.join(', ') : 'нет'}</Row>
                <Row label="DNS: ожидаются" mono>{dns.expected.length ? dns.expected.join(', ') : '—'}</Row>
                {dns.aaaa.length > 0 && <Row label="DNS: AAAA" mono tone="warning">{dns.aaaa.join(', ')}</Row>}
              </>
            )}
          </>
        )}
      </div>
      {dns && !dns.ok && <Box tone="warning">{dns.problem}</Box>}
    </section>
  )
}

function RunSection({ run, openStep, onToggle }) {
  const v = runView(run)
  return (
    <section className="space-y-2" aria-label="Последний запуск">
      <SectionTitle>Последний запуск</SectionTitle>
      <div className="flex flex-wrap items-center gap-2" aria-live="polite">
        <span className="text-sm text-primary">{v.kind}</span>
        <Badge variant={v.variant}>
          {run.status === 'running' && <Spinner size="xs" />}
          {v.label}
        </Badge>
      </div>
      <div>
        <Row label="Начат" mono>{fmtDateTime(run.started_at)}</Row>
        <Row label="Завершён" mono>{run.status === 'running' ? 'ещё идёт' : fmtDateTime(run.finished_at)}</Row>
      </div>
      {run.error && <Box tone="danger">{run.error}</Box>}
      <StepList steps={run.steps} open={openStep} onToggle={onToggle} />
    </section>
  )
}

/**
 * WebProxyCard — the Telegram WEB proxy (official tproxy-server + MTProxy on the Hub): on/off, address,
 * «Установить / обновить», «Сменить секрет», live state and the last install/disable run.
 * GET every 3 s while a run is in progress, else every 30 s while the tab is visible (and at once when it
 * becomes visible again). Edits live in a draft, so polling never overwrites what the owner is typing.
 * The API never returns the secret, and this card never asks for it.
 */
export default function WebProxyCard() {
  const { can } = useAuth()
  const toast = useToast()
  const uid = useId()
  const canEdit = can('settings.write')
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const [draft, setDraft] = useState(null) // null = no edits: the form shows the saved values
  const [busy, setBusy] = useState('') // '' | save | install | rotate
  const [confirm, setConfirm] = useState('') // '' | off | rotate
  const [stepChoice, setStepChoice] = useState({ run: '', open: undefined })
  // A newer request (a poll or an action) wins: an older answer arriving later is dropped.
  const [nextRequest] = useState(() => requestSequencer())

  const load = useCallback(async ({ manual = false } = {}) => {
    const isCurrent = nextRequest()
    if (manual) setRefreshing(true)
    try {
      const d = await fetchTGWebProxy()
      if (isCurrent()) { setData(d); setError('') }
    } catch (e) {
      if (isCurrent()) setError(e?.message || 'ошибка')
    } finally {
      if (manual) setRefreshing(false)
    }
  }, [nextRequest])

  const running = isRunning(data)
  useEffect(() => { load() }, [load])
  useEffect(() => {
    const id = setInterval(() => {
      if (running || document.visibilityState === 'visible') load()
    }, running ? RUN_POLL_MS : IDLE_POLL_MS)
    return () => clearInterval(id)
  }, [running, load])
  useEffect(() => {
    const onVisible = () => { if (document.visibilityState === 'visible') load() }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [load])

  // act: one PUT/POST; every one answers with the full GET payload. 409 = a run is already going.
  async function act(kind, call, okText) {
    setBusy(kind)
    const isCurrent = nextRequest()
    try {
      const d = await call()
      if (isCurrent() && d) { setData(d); setError('') }
      toast.success(okText)
      return true
    } catch (e) {
      toast.error(e?.message || 'ошибка')
      if (e?.status === 409) load()
      return false
    } finally {
      setBusy('')
    }
  }

  const form = draft ?? formFrom(data)
  const set = (patch) => setDraft({ ...form, ...patch })
  const check = saveCheck(form, data, { canEdit })

  async function save() {
    const body = payloadFrom(form, data)
    if (await act('save', () => saveTGWebProxy(body), saveMessage(data, body))) {
      setDraft(null)
      setConfirm('')
    }
  }
  const onSave = () => (check.confirmOff ? setConfirm('off') : save())
  const install = () => act('install', installTGWebProxy, 'Установка запущена')
  async function rotate() {
    if (await act('rotate', rotateTGWebProxySecret, 'Новый секрет сохранён — relay и MTProxy перезапускаются')) setConfirm('')
  }
  const closeConfirm = () => { if (!busy) setConfirm('') }

  // The expanded step: automatic (first problem, else the running one) until the owner picks, per run.
  const run = data?.run || null
  const runId = run ? `${run.kind}|${run.started_at}` : ''
  const openStep = stepChoice.run === runId && stepChoice.open !== undefined ? stepChoice.open : autoOpenStep(run)
  const toggleStep = (key) => setStepChoice({ run: runId, open: openStep === key ? null : key })

  if (!data && !error) {
    return (
      <Card>
        <CardHeader><Skeleton className="h-5 w-40" /></CardHeader>
        <CardBody className="space-y-3"><Skeleton className="h-10" /><Skeleton className="h-48" /></CardBody>
      </Card>
    )
  }
  if (!data) {
    return (
      <div role="alert" className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
        <span className="text-sm text-danger">Не удалось загрузить WEB-прокси: {error}</span>
        <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={() => load({ manual: true })}
          loading={refreshing}>Повторить</Button>
      </div>
    )
  }

  const state = overallState(data)
  const hostErr = hostError(form.host)
  const locked = running || !canEdit || !!busy
  const lockHint = !canEdit
    ? 'Менять настройки может администратор с правом settings.write.'
    : running ? 'Идёт запуск — кнопки станут доступны, когда он закончится.' : ''
  const offHost = effectiveHost(data.host, data.default_host)

  return (
    <Card>
      <CardHeader className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-semibold text-primary">WEB-прокси</h2>
        <Badge variant={state.variant}>{state.label}</Badge>
      </CardHeader>
      <CardBody className="space-y-5">
        <div className="space-y-1">
          <p className="text-xs text-secondary">
            Telegram Desktop 7.1+ и Android (обновлённый Telegram) подключаются к нему как к обычному сайту по HTTPS.
            iOS пока не поддерживает — для него остаётся классический MTProto ниже.
          </p>
          {state.hint && <p className="text-xs text-warning">{state.hint}</p>}
        </div>

        <Switch checked={form.enabled} disabled={!canEdit} onChange={(v) => set({ enabled: v })} label="Включить"
          description="Включённый: подписчики видят ссылку в кабинете (после успешной установки на текущий адрес)." />

        <div>
          <label htmlFor={`${uid}-host`} className="text-xs text-secondary block mb-1">Адрес</label>
          <Input id={`${uid}-host`} value={form.host} disabled={!canEdit} onChange={(e) => set({ host: e.target.value })}
            className="font-mono" placeholder={data.default_host || 'tg.example.ru'} error={!!hostErr}
            autoCapitalize="none" autoCorrect="off" spellCheck={false} aria-invalid={hostErr ? true : undefined}
            aria-describedby={`${uid}-host-hint`} />
          {hostErr && <p className="text-xs text-danger mt-1">{hostErr}</p>}
          <p id={`${uid}-host-hint`} className="text-xs text-tertiary mt-1">
            A-записи адреса — те же, что у go.&lt;домен&gt; (входы в РФ). Сменили адрес — нужен новый сертификат: он выпустится при установке.
          </p>
        </div>

        {canEdit && check.dirty && (
          <div className="flex flex-col sm:flex-row sm:items-center gap-2">
            <Button onClick={onSave} loading={busy === 'save'} disabled={!check.canSave || !!busy} className="w-full sm:w-auto">Сохранить</Button>
            <Button variant="ghost" onClick={() => setDraft(null)} disabled={!!busy} className="w-full sm:w-auto">Отменить</Button>
            {check.reason && !hostErr && <span className="text-xs text-tertiary">{check.reason}</span>}
          </div>
        )}

        <div className="space-y-2">
          <div className="flex flex-col sm:flex-row sm:flex-wrap gap-2">
            <Button variant="secondary" icon={<Download className="w-3.5 h-3.5" />} onClick={install} loading={busy === 'install'}
              disabled={locked} className="w-full sm:w-auto">Установить / обновить</Button>
            <Button variant="secondary" className="w-full sm:w-auto text-danger" icon={<KeyRound className="w-3.5 h-3.5" />}
              onClick={() => setConfirm('rotate')} disabled={locked}>Сменить секрет</Button>
          </div>
          {lockHint && <p className="text-xs text-tertiary">{lockHint}</p>}
        </div>

        <StatusSection data={data} error={error} refreshing={refreshing} onRefresh={() => load({ manual: true })} />
        {run && <RunSection run={run} openStep={openStep} onToggle={toggleStep} />}
      </CardBody>

      <Dialog open={confirm === 'off'} onClose={closeConfirm} title="Выключить WEB-прокси?" maxWidth="max-w-md">
        <ConfirmBody busy={busy === 'save'} onCancel={closeConfirm} onConfirm={save} actionLabel="Выключить"
          text={`Ссылка на WEB-прокси исчезнет из кабинетов подписчиков, relay и MTProxy на Hub остановятся, блок nginx для ${offHost} будет удалён. Включить обратно можно в любой момент — запустится новая установка.`} />
      </Dialog>
      <Dialog open={confirm === 'rotate'} onClose={closeConfirm} title="Сменить секрет?" maxWidth="max-w-md">
        <ConfirmBody busy={busy === 'rotate'} onCancel={closeConfirm} onConfirm={rotate} actionLabel="Сменить секрет"
          text="Все выданные ссылки перестанут работать; подписчикам придётся подключить WEB-прокси заново из кабинета; relay и MTProxy перезапустятся." />
      </Dialog>
    </Card>
  )
}
