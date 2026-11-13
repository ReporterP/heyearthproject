import { useEffect, useId, useReducer, useRef, useState } from 'react'
import { ShieldCheck, X } from 'lucide-react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { probeNode, createNode, preflightNode, provisionNode, updateNodeRouting, fetchPlans, saveEntryMode } from '../../api/admin.js'
import { Dialog, Stepper, Button, Input, Select, Switch, CopyButton, Badge, Skeleton, Spinner, useFocusTrap } from '../ui/index.js'
import { keyTypeLabel, hostKeyFile, PRIORITY_HINT } from './nodeModel.js'
import MobileCheckPanel from '../entries/MobileCheckPanel.jsx'
import PrepareServer from '../servers/PrepareServer.jsx'
import { exitCountries, geoHint, countryLabel } from '../servers/serversModel.js'
import {
  wizardSteps, INSTALL_STEP, initialWizard, wizardReducer, canGo, parsePort, countryValid, matchPasted,
  pasteVerdict, canConfirmKey, canCreate, canStart, passwordFlags, probeRequest, createRequest, provisionRequest, routingRequest,
  closeNeedsConfirm,
} from './wizardModel.js'
import JobProgress from './JobProgress.jsx'

const PRIORITIES = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]

function Field({ label, hint, error, htmlFor, className = 'mb-3', children }) {
  return (
    <div className={className}>
      <label htmlFor={htmlFor} className="block text-xs text-secondary mb-1">{label}</label>
      {children}
      {error ? <p className="text-xs text-danger mt-1">{error}</p> : hint && <p className="text-xs text-tertiary mt-1">{hint}</p>}
    </div>
  )
}

function StepHeading({ headingRef, title, children }) {
  return (
    <div className="mb-4">
      <h3 ref={headingRef} tabIndex={-1} className="text-sm font-medium text-primary focus:outline-none">{title}</h3>
      {children && <p className="text-xs text-tertiary mt-1">{children}</p>}
    </div>
  )
}

// RoleStep: the role and the country of the server.
function RoleStep({ s, set, ids, countryRef }) {
  const countryBad = s.country.length === 2 && !countryValid(s.country)
  return (
    <div>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 mb-4" role="group" aria-label="Роль ноды">
        <button type="button" aria-pressed={s.role === 'exit'} onClick={() => set({ role: 'exit' })}
          className={`text-left p-3 rounded border transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent
            ${s.role === 'exit' ? 'border-accent bg-accent/10' : 'border-default hover:bg-hover'}`}>
          <div className="text-sm text-primary">Выход за рубежом</div>
          <div className="text-xs text-tertiary mt-1">Через него трафик уходит в интернет. Нужен VPS вне РФ: Ubuntu или Debian, x86_64.</div>
        </button>
        <button type="button" aria-pressed={s.role === 'relay'} disabled={!!s.serverId} onClick={() => set({ role: 'relay' })}
          className={`text-left p-3 rounded border transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent
            ${s.role === 'relay' ? 'border-accent bg-accent/10' : 'border-default hover:bg-hover'}`}>
          <div className="text-sm text-primary">Вход в РФ (для мобильных)</div>
          <div className="text-xs text-tertiary mt-1">Российский VPS другого провайдера — адрес, к которому подключаются пользователи. Пересылает подключения на Hub, своих данных не хранит.</div>
        </button>
      </div>
      {s.role === 'relay' && (
        <p className="mb-4 text-xs text-secondary">
          Нужен чистый сервер Ubuntu 24.04 у провайдера, где нет Hub. Перед настройкой мастер предложит проверить адрес с телефона:
          мобильные операторы пропускают не все подсети хостингов.
        </p>
      )}
      {!s.serverId && (
        <div className="mb-4">
          <h4 className="text-sm text-primary mb-2">Подготовьте сервер у хостера</h4>
          <PrepareServer role={s.role} />
        </div>
      )}
      <Field label="Страна сервера" htmlFor={ids.country} error={countryBad ? 'Выберите страну из списка' : ''}
        hint={s.role === 'relay' ? 'Вход для пользователей из России ставится в России.' : 'Где стоит сервер (по данным хостера). После проверки подключения мастер подскажет страну по реестру IP-адресов.'}>
        {s.role === 'relay' ? (
          <Input id={ids.country} ref={countryRef} value={countryLabel('RU')} disabled className="w-full sm:w-64" />
        ) : (
          <Select id={ids.country} ref={countryRef} value={s.country} disabled={!!s.serverId} onChange={(e) => set({ country: e.target.value })}
            className="w-full sm:w-64" aria-invalid={countryBad || undefined}>
            <option value="">Выберите страну…</option>
            {exitCountries(s.country).map((c) => <option key={c.code} value={c.code}>{c.label}</option>)}
          </Select>
        )}
      </Field>
      <Field label="Название (необязательно)" htmlFor={ids.name}
        hint={s.role === 'relay'
          ? 'Например, «Москва Timeweb». По нему вы отличаете входы и подтверждаете опасные действия. Пусто — «Резервный вход RU».'
          : `Например, «Франкфурт». По нему вы отличаете ноды и подтверждаете опасные действия. Пусто — «Выход ${s.country || 'DE'}».`}>
        <Input id={ids.name} value={s.name} disabled={!!s.serverId} maxLength={64} onChange={(e) => set({ name: e.target.value })} />
      </Field>
      {s.serverId && <p className="text-xs text-tertiary">Нода уже сохранена: название и страну можно изменить позже в её карточке.</p>}
    </div>
  )
}

// KeyChooser: every host key the server presented, how to compare it in the provider's console,
// an optional paste of the console output and the explicit confirmation.
function KeyChooser({ s, dispatch, ids, busy }) {
  const keys = s.probe?.keys || []
  const saved = !!s.serverId
  const chosen = keys.find((k) => k.fingerprint === s.fingerprint)
  const verdict = pasteVerdict(s)
  const pastedKey = verdict === 'mismatch' ? matchPasted(s.pasted, keys) : null
  return (
    <div className="mt-4 p-3 rounded border border-default bg-elevated">
      <div className="flex items-center gap-2 text-sm text-primary mb-1">
        <ShieldCheck className="w-4 h-4 text-accent" aria-hidden="true" />
        Ключи SSH, которые предъявил сервер
      </div>
      {!saved && (
        <p className="text-xs text-tertiary mb-3">
          Откройте консоль сервера в панели хостера (веб-консоль или VNC, не SSH) и выполните команду для любого из ключей.
          Отпечаток должен совпасть до символа — иначе подключение кто-то подменяет, и пароль ему отдавать нельзя.
        </p>
      )}
      <div role="radiogroup" aria-label="Ключ для сверки" className="space-y-2">
        {keys.map((k) => {
          const selected = k.fingerprint === s.fingerprint
          const file = hostKeyFile(k.type)
          const cmd = file ? `ssh-keygen -lf ${file}` : ''
          return (
            <div key={k.fingerprint}
              className={`rounded border px-3 py-2 ${selected ? 'border-accent bg-accent/10' : 'border-default'} ${saved && !selected ? 'opacity-50' : ''}`}>
              <div className="flex items-start gap-1">
                <label className={`flex items-start gap-2 min-w-0 flex-1 ${saved ? '' : 'cursor-pointer'}`}>
                  <input type="radio" name={ids.keys} checked={selected} disabled={saved || !!busy} className="mt-1 accent-accent"
                    onChange={() => dispatch({ type: 'selectKey', fingerprint: k.fingerprint })} />
                  <span className="min-w-0 flex-1">
                    <span className="block text-xs text-secondary">{keyTypeLabel(k.type) || 'Ключ'}</span>
                    <code className="block font-mono text-xs sm:text-sm text-primary break-all">{k.fingerprint}</code>
                  </span>
                </label>
                <CopyButton text={k.fingerprint} label={`Скопировать отпечаток ${keyTypeLabel(k.type)}`} />
              </div>
              {cmd && !saved && (
                <div className="flex items-center gap-1 mt-1 pl-6">
                  <code className="font-mono text-xs text-tertiary break-all">{cmd}</code>
                  <CopyButton text={cmd} label="Скопировать команду" />
                </div>
              )}
            </div>
          )
        })}
      </div>

      {saved ? (
        <div className="mt-3"><Badge variant="success">ключ подтверждён и сохранён</Badge></div>
      ) : (
        <>
          <Field label="Вывод команды из консоли хостера (необязательно)" htmlFor={ids.paste} className="mt-3 mb-3"
            hint="Вставьте строку целиком — мастер выберет ключ с этим отпечатком; подтверждаете вы сами, переключателем ниже.">
            <Input id={ids.paste} value={s.pasted} disabled={!!busy} onChange={(e) => dispatch({ type: 'paste', text: e.target.value })}
              placeholder="256 SHA256:… root@server (ED25519)" autoComplete="off" spellCheck={false} className="font-mono text-xs" />
          </Field>
          {verdict === 'match' && (
            <p className="-mt-2 mb-3 text-xs text-success">Совпадает с ключом {keyTypeLabel(chosen?.type)} — ключ выбран. Подтвердите его переключателем ниже.</p>
          )}
          {verdict === 'mismatch' && (
            <p role="alert" className="-mt-2 mb-3 text-xs text-danger">
              {pastedKey
                ? `Это отпечаток ключа ${keyTypeLabel(pastedKey.type)}, а выбран ${keyTypeLabel(chosen?.type)} — выберите ключ, который вы сверяли.`
                : 'Не совпадает ни с одним ключом, который предъявил сервер. Не сохраняйте ноду: проверьте IP-адрес — или подключение кто-то подменяет.'}
            </p>
          )}
          <Switch checked={s.fingerprintConfirmed} disabled={!!busy || !canConfirmKey(s)}
            onChange={(v) => dispatch({ type: 'confirmKey', value: v })}
            label={`Отпечаток ${keyTypeLabel(chosen?.type) || 'ключа'} совпадает с консолью хостера`}
            description="Подтверждая, вы разрешаете Hub подключаться к серверу только с этим ключом." />
        </>
      )}
    </div>
  )
}

// AccessStep: address, root password, the probe and the key confirmation.
function AccessStep({ s, set, dispatch, ids, busy, onProbe, passwordRef }) {
  const saved = !!s.serverId
  const portBad = parsePort(s.sshPort) === null
  return (
    <div>
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-x-2">
        <div className="sm:col-span-2">
          <Field label="IP-адрес сервера" htmlFor={ids.ip}>
            <Input id={ids.ip} value={s.ip} disabled={saved || !!busy} autoComplete="off" autoCapitalize="none" spellCheck={false}
              placeholder="203.0.113.10" onChange={(e) => set({ ip: e.target.value })} className="font-mono" />
          </Field>
        </div>
        <Field label="SSH-порт" htmlFor={ids.port} error={portBad ? 'От 1 до 65535' : ''}>
          <Input id={ids.port} value={s.sshPort} disabled={saved || !!busy} inputMode="numeric" autoComplete="off" placeholder="22"
            onChange={(e) => set({ sshPort: e.target.value })} className="font-mono tabular-nums" aria-invalid={portBad || undefined} />
        </Field>
      </div>
      {!saved && <p className="mb-3 text-xs text-tertiary">Ключ Hub и порты — на первом шаге «Подготовка». Если ключ вставлен у хостера, пароль ниже не нужен.</p>}
      {saved ? (
        <p className="mb-3 text-xs text-tertiary">Доступ сохранён: Hub входит по своему ключу; пароль, если был, хранится зашифрованным и больше не показывается.</p>
      ) : (
        <Field label="Пароль root (необязательно)" htmlFor={ids.password}
          hint="Не нужен, если ключ Hub уже вставлен у хостера. Иначе — пароль root: отправляется только при сохранении, хранится зашифрованным, после настройки остаётся запасным.">
          {/* Uncontrolled on purpose: the password is never in React state, a value attribute or an action. */}
          <Input id={ids.password} ref={passwordRef} type="password" name={ids.password} autoComplete="new-password" spellCheck={false}
            defaultValue="" disabled={!!busy} onChange={(e) => dispatch({ type: 'passwordFlags', ...passwordFlags(e.target.value) })} />
        </Field>
      )}
      {!saved && (
        <div className="flex flex-col sm:flex-row sm:items-center gap-2">
          <Button variant="secondary" onClick={onProbe} loading={busy === 'probe'} disabled={!!busy || !probeRequest(s)} className="w-full sm:w-auto">
            {s.probe ? 'Проверить подключение ещё раз' : 'Проверить подключение'}
          </Button>
          <span className="text-xs text-tertiary">Только обмен ключами SSH — пароль на этом шаге не отправляется.</span>
        </div>
      )}
      {s.probe && <GeoHint s={s} set={set} />}
      {s.probe && <KeyChooser s={s} dispatch={dispatch} ids={ids} busy={busy} />}
    </div>
  )
}

// GeoHint: the registry country of the probed address next to the chosen one (B2). An exit's
// country can still be changed until the node is saved; an entry must be a Russian address.
function GeoHint({ s, set }) {
  const h = geoHint(s.country, s.probe?.geo_country)
  if (!h) return null
  if (s.role === 'relay') {
    return h.code === 'RU' ? null : (
      <p className="mt-3 text-xs text-warning">По реестру адресов этот IP — {countryLabel(h.code)}, а вход в РФ должен быть российским адресом: мобильные операторы пропускают отечественные подсети.</p>
    )
  }
  return (
    <div className={`mt-3 flex flex-col sm:flex-row sm:items-center gap-2 text-xs ${h.matches ? 'text-success' : 'text-warning'}`}>
      <span>{h.text}{!h.matches && s.country ? `, а выбрано ${countryLabel(s.country)}` : ''}.</span>
      {!h.matches && !s.serverId && h.code !== 'RU' && (
        <Button size="sm" variant="secondary" onClick={() => set({ country: h.code })}>Выбрать {countryLabel(h.code)}</Button>
      )}
    </div>
  )
}

function PreflightStep({ s, busy, onRun }) {
  const running = busy === 'preflight'
  const pf = s.preflight
  return (
    <div>
      {running && (
        <p className="flex items-center gap-2 text-sm text-secondary mb-3" role="status">
          <Spinner size="sm" /> Проверяю сервер: ОС, архитектура, вход под root, диск, часы, доступ в интернет, свободные порты… (до минуты)
        </p>
      )}
      {pf && (
        <ul className="border border-default rounded">
          {pf.checks.map((c, i) => (
            <li key={i} className="flex items-start sm:items-center gap-3 min-h-9 px-3 py-1.5 border-b border-default last:border-b-0">
              <Badge variant={c.ok ? 'success' : 'danger'} className="shrink-0">{c.ok ? 'ок' : 'нет'}</Badge>
              <div className="flex-1 min-w-0 flex flex-col sm:flex-row sm:items-center sm:gap-3">
                <span className="text-sm text-primary sm:shrink-0">{c.name}</span>
                {c.detail && <span className="text-xs text-tertiary break-words sm:truncate" title={c.detail}>{c.detail}</span>}
              </div>
            </li>
          ))}
        </ul>
      )}
      {pf && pf.ok && <p className="mt-3 text-sm text-success">Сервер подходит.</p>}
      {pf && !pf.ok && <p className="mt-3 text-sm text-danger">Сервер не подходит — исправьте отмеченное и проверьте ещё раз.</p>}
      {!running && (
        <Button variant="secondary" className="mt-3 w-full sm:w-auto" onClick={onRun} disabled={!!busy}>
          {pf ? 'Проверить ещё раз' : 'Проверить сервер'}
        </Button>
      )}
    </div>
  )
}

function OptionsStep({ s, set, dispatch, ids }) {
  return (
    <div className="space-y-5">
      <Switch checked={s.mtproxy} onChange={(v) => set({ mtproxy: v })} label="Telegram-прокси (MTProxy)"
        description="Пользователи получат прокси для Telegram на этой ноде. Включайте, только если A-запись домена маскировки уже указывает на IP ноды — иначе установка прокси не пройдёт." />
      <fieldset>
        <legend className="text-xs text-secondary mb-1">Тарифы, которые идут через эту ноду</legend>
        {s.plansState === 'loading' && <Skeleton className="h-16 w-full" />}
        {s.plansState === 'failed' && (
          <p className="text-xs text-warning">
            Тарифы не загрузились ({s.plansError}). Нода получит только общий пул — тарифы можно привязать позже в карточке ноды.
          </p>
        )}
        {s.plansState === 'ready' && s.plans.length === 0 && <p className="text-xs text-tertiary">Тарифов пока нет — нода получит общий пул.</p>}
        {s.plansState === 'ready' && s.plans.length > 0 && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-3">
            {s.plans.map((p) => (
              <label key={p.id} className="flex items-center gap-2 min-h-9 text-sm text-primary cursor-pointer">
                <input type="checkbox" className="accent-accent" checked={(s.planIds || []).includes(p.id)}
                  onChange={(e) => dispatch({ type: 'togglePlan', id: p.id, on: e.target.checked })} />
                <span className="min-w-0 truncate">{p.name}</span>
                <span className="font-mono tabular-nums text-xs text-tertiary whitespace-nowrap">{p.price_rub} ₽</span>
                {!p.is_active && <Badge>не продаётся</Badge>}
              </label>
            ))}
          </div>
        )}
        {s.plansState === 'ready' && s.plans.length > 0 && (
          <p className="text-xs text-tertiary mt-1">По умолчанию — все платные в продаже. Без привязки нода получает только общий пул.</p>
        )}
      </fieldset>
      <Field label="Приоритет (1–10)" htmlFor={ids.priority} hint={`${PRIORITY_HINT}.`}>
        <Select id={ids.priority} value={String(s.loadWeight)} onChange={(e) => set({ loadWeight: Number(e.target.value) })}
          className="font-mono tabular-nums w-24">
          {PRIORITIES.map((n) => <option key={n} value={n}>{n}</option>)}
        </Select>
      </Field>
      <Switch checked={s.fallback} onChange={(v) => set({ fallback: v })} label="Запасной TCP-путь"
        description="Если основной туннель (UDP) не отвечает — например, его душат на пути «Россия → заграница», — трафик этого выхода идёт по TCP. Без него молчащий основной туннель не даст выходу включиться." />
    </div>
  )
}

/**
 * AddNodeWizard — «Добавить ноду»: Роль и страна → Доступ и ключ (probe, every host key, explicit
 * confirmation, createNode) → Предпроверка → Настройки (MTProxy, тарифы, приоритет, запасной TCP) →
 * Установка (provisionNode + JobProgress with the final path check).
 * Props: initialRole ('exit' | 'relay' — the «Ноды» tab; relay comes with phase 3), onClose, onOpenNode(id).
 * The state machine and the requests live in wizardModel.js (node --test).
 */
export default function AddNodeWizard({ initialRole = 'exit', onClose, onOpenNode }) {
  const { can } = useAuth()
  const [s, dispatch] = useReducer(wizardReducer, initialRole, initialWizard)
  const [busy, setBusy] = useState('') // '' | probe | create | preflight | start
  const [error, setError] = useState('')
  const [result, setResult] = useState(null)
  const [confirmingClose, setConfirmingClose] = useState(false)
  const [mobile, setMobile] = useState({ state: 'none' }) // the phone check of a reserve entry
  const [enabled, setEnabled] = useState('') // '' | done: everyone was sent through this entry
  const uid = useId()
  const ids = {
    title: `${uid}-title`, country: `${uid}-country`, name: `${uid}-name`, ip: `${uid}-ip`, port: `${uid}-port`,
    password: `${uid}-pw`, keys: `${uid}-keys`, paste: `${uid}-paste`, priority: `${uid}-priority`,
  }
  const rootRef = useRef(null)
  const countryRef = useRef(null)
  const headingRef = useRef(null)
  const stayRef = useRef(null)
  const passwordRef = useRef(null)
  const busyRef = useRef(false)
  const autoPreflight = useRef(false)
  const firstStep = useRef(true)
  const set = (patch) => dispatch({ type: 'set', patch })

  function requestClose() {
    if (confirmingClose) { setConfirmingClose(false); return }
    if (closeNeedsConfirm(s)) setConfirmingClose(true)
    else onClose()
  }
  useFocusTrap(rootRef, true, { initialFocus: countryRef, onEscape: requestClose })
  useEffect(() => { if (confirmingClose) stayRef.current?.focus() }, [confirmingClose])

  useEffect(() => {
    let alive = true
    fetchPlans()
      .then((p) => { if (alive) dispatch({ type: 'plans', plans: p }) })
      .catch((e) => { if (alive) dispatch({ type: 'plansFailed', error: e.message }) })
    return () => { alive = false }
  }, [])

  // A new step: its heading takes the focus (not on open — the trap focuses the country field).
  useEffect(() => {
    if (firstStep.current) { firstStep.current = false; return }
    headingRef.current?.focus()
    setError('')
    setConfirmingClose(false)
  }, [s.step])

  // One request at a time; the server's text is shown as it is (apiFetch keeps it, 403 included).
  async function act(kind, fn) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(kind)
    setError('')
    try {
      await fn()
    } catch (e) {
      setError(e?.message || 'Не удалось выполнить действие')
    } finally {
      busyRef.current = false
      setBusy('')
    }
  }

  const probe = () => act('probe', async () => {
    const req = probeRequest(s)
    if (!req) return
    const r = await probeNode(req)
    dispatch({ type: 'probed', ip: req.ip_address, port: req.ssh_port, result: r })
  })
  const create = () => act('create', async () => {
    if (!canCreate(s)) return
    const r = await createNode(createRequest(s, passwordRef.current?.value))
    if (passwordRef.current) passwordRef.current.value = ''
    dispatch({ type: 'created', id: r.id })
  })
  const runPreflight = () => act('preflight', async () => {
    dispatch({ type: 'preflightStarted' })
    dispatch({ type: 'preflight', result: await preflightNode(s.serverId) })
  })
  const start = () => act('start', async () => {
    if (!canStart(s)) return
    const routing = routingRequest(s)
    if (routing) {
      await updateNodeRouting(s.serverId, routing)
      dispatch({ type: 'fallbackStored', value: routing.tunnel_fallback_enabled })
    }
    const r = await provisionNode(s.serverId, provisionRequest(s))
    dispatch({ type: 'started', jobId: r.job_id })
  })

  // Entering «Предпроверка» without a result runs it once by itself; a failure waits for «Проверить».
  useEffect(() => {
    if (s.step !== 2) { autoPreflight.current = false; return }
    if (!s.serverId || s.preflight || busy || autoPreflight.current) return
    autoPreflight.current = true
    runPreflight()
  })

  const enableForAll = () => act('enable', async () => {
    await saveEntryMode('relay', s.serverId)
    setEnabled('done')
  })

  const go = (step) => dispatch({ type: 'go', step })
  const relay = s.role === 'relay'
  const steps = wizardSteps(s.role)
  const installing = s.step === INSTALL_STEP
  const succeeded = result?.status === 'succeeded'
  const back = (
    <Button variant="ghost" onClick={s.step === 0 ? requestClose : () => go(s.step - 1)} disabled={!!busy}>
      {s.step === 0 ? 'Отмена' : 'Назад'}
    </Button>
  )
  let primary
  if (s.step === 1 && !s.serverId) {
    primary = <Button onClick={create} loading={busy === 'create'} disabled={!!busy || !canCreate(s)}>Подтвердить и сохранить</Button>
  } else if (s.step === 3 && relay) {
    primary = (
      <Button onClick={start} loading={busy === 'start'} disabled={!!busy || !canStart(s)}
        variant={mobile.state === 'fail' ? 'secondary' : 'primary'}>
        {mobile.state === 'pass' ? 'Настроить вход' : 'Настроить без проверки'}
      </Button>
    )
  } else if (s.step === 3) {
    primary = (
      <Button onClick={start} loading={busy === 'start'} disabled={!!busy || !canStart(s)}
        title={s.plansState === 'loading' ? 'Загружаются тарифы…' : undefined}>Установить</Button>
    )
  } else {
    primary = <Button onClick={() => go(s.step + 1)} disabled={!!busy || !canGo(s, s.step + 1)}>Далее</Button>
  }

  return (
    <Dialog open onClose={requestClose} maxWidth="max-w-2xl">
      <div ref={rootRef} role="dialog" aria-modal="true" aria-labelledby={ids.title}>
        <div className="flex items-center justify-between gap-2 mb-4">
          <h2 id={ids.title} className="text-lg font-semibold text-primary tracking-tight">{relay ? 'Вход в РФ для мобильных' : 'Выход за рубежом'}</h2>
          <button type="button" onClick={requestClose} aria-label="Закрыть"
            className="inline-flex items-center justify-center w-8 h-8 rounded text-tertiary hover:text-primary hover:bg-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent">
            <X className="w-4 h-4" aria-hidden="true" />
          </button>
        </div>
        <Stepper steps={steps} current={installing && succeeded ? steps.length : s.step} />

        {s.step === 0 && (
          <>
            <StepHeading headingRef={headingRef} title="Подготовка">Что это за сервер, что вставить у хостера и где он стоит.</StepHeading>
            <RoleStep s={s} set={set} ids={ids} countryRef={countryRef} />
          </>
        )}
        {s.step === 1 && (
          <>
            <StepHeading headingRef={headingRef} title="Доступ и ключ">
              Hub подключится к серверу только после того, как вы сверите отпечаток его ключа с панелью хостера.
            </StepHeading>
            <AccessStep s={s} set={set} dispatch={dispatch} ids={ids} busy={busy} onProbe={probe} passwordRef={passwordRef} />
          </>
        )}
        {s.step === 2 && (
          <>
            <StepHeading headingRef={headingRef} title="Предпроверка">
              Только чтение: на сервере ничего не меняется.{relay ? ' Нужен чистый сервер: порты 443, 2443 и UDP 50443 свободны.' : ''}
            </StepHeading>
            <PreflightStep s={s} busy={busy} onRun={runPreflight} />
          </>
        )}
        {s.step === 3 && relay && (
          <>
            <StepHeading headingRef={headingRef} title="Проверка с телефона">
              Убедитесь, что мобильные операторы пропускают этот адрес — тогда настройка не пропадёт зря.
            </StepHeading>
            <MobileCheckPanel serverId={s.serverId} canStart={can('nodes.provision')} onResult={setMobile} />
            {mobile.state === 'fail' && (
              <p className="mt-3 text-xs text-warning">Адрес не проходит: настраивать его для мобильных бессмысленно. Смените IP у хостера, закройте мастер и добавьте сервер заново (старую запись удалите в «Нодах»).</p>
            )}
          </>
        )}
        {s.step === 3 && !relay && (
          <>
            <StepHeading headingRef={headingRef} title="Настройки">Всё это можно изменить позже в карточке ноды.</StepHeading>
            <OptionsStep s={s} set={set} dispatch={dispatch} ids={ids} />
          </>
        )}
        {installing && s.jobId != null && (
          <>
            <StepHeading headingRef={headingRef} title="Установка">
              Нода войдёт в маршрутизацию только после проверки путей туннеля.
            </StepHeading>
            <JobProgress jobId={s.jobId} canProvision={can('nodes.provision')} tcpEnabled={!relay && s.fallback} onFinished={setResult} />
            {!result && <p className="text-xs text-tertiary mt-3">Окно можно закрыть: установка продолжится, прогресс — в карточке ноды → «Задачи».</p>}
            {relay && succeeded && (
              <div className="mt-4 rounded border border-accent/30 bg-accent/10 p-3 space-y-2">
                {enabled ? (
                  <p className="text-sm text-success">Готово: подписка у всех пользователей теперь ведёт на этот вход. Приложения подхватят её при следующем обновлении (до 4 часов) или сразу — кнопкой обновления подписки.</p>
                ) : (
                  <>
                    <p className="text-sm text-primary">Вход настроен. Последний шаг — перевести на него всех пользователей.</p>
                    <p className="text-xs text-secondary">Hub останется в подписке строками «· напрямую». Вернуть всё как было можно на странице «Входы» в любой момент.
                      Если кнопка ответит «не отвечает снаружи» — монитор ещё проверяет вход, подождите 2 минуты.</p>
                    {can('nodes.provision') && (
                      <Button onClick={enableForAll} loading={busy === 'enable'} disabled={!!busy}>Включить для всех пользователей</Button>
                    )}
                  </>
                )}
              </div>
            )}
          </>
        )}

        {error && <p role="alert" className="mt-3 text-sm text-danger break-words">{error}</p>}

        {confirmingClose ? (
          <div role="alertdialog" aria-label="Закрыть мастер?" className="mt-6 rounded border border-warning/30 bg-warning/10 p-3">
            <p className="text-sm text-primary">
              {s.serverId
                ? 'Закрыть мастер? Нода сохранена и ждёт настройки — продолжить можно позже в её карточке.'
                : 'Закрыть мастер? Введённые данные, включая пароль, не сохранятся.'}
            </p>
            <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2 mt-3">
              <Button ref={stayRef} variant="secondary" onClick={() => setConfirmingClose(false)}>Остаться</Button>
              <Button variant="danger" onClick={onClose}>Закрыть мастер</Button>
            </div>
          </div>
        ) : installing ? (
          <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2 mt-6">
            <Button variant={succeeded ? 'ghost' : 'secondary'} onClick={onClose}>Закрыть</Button>
            <Button variant={succeeded ? 'primary' : 'secondary'} onClick={() => onOpenNode(s.serverId)}>Открыть карточку ноды</Button>
          </div>
        ) : (
          <div className="flex flex-col-reverse sm:flex-row sm:justify-between gap-2 mt-6">
            {back}
            {primary}
          </div>
        )}
      </div>
    </Dialog>
  )
}
