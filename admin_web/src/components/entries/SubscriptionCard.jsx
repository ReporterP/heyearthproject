import { useEffect, useState } from 'react'
import { CheckCircle2, Globe, TriangleAlert, Zap } from 'lucide-react'
import { useToast } from '../Toast.jsx'
import { saveEntrySettings } from '../../api/admin.js'
import { formatDate } from '../../utils/format.js'
import { Badge, Button, Card, CardHeader, CardBody, Input, Table, Thead, Tbody, Tr, Th, Td } from '../ui/index.js'
import { AUTO_MODES, subscriptionDomainState, suggestedSubDomain } from './entriesModel.js'

const TONE = {
  success: 'border-success/30 bg-success/10 text-success',
  warning: 'border-warning/30 bg-warning/10 text-warning',
  danger: 'border-danger/30 bg-danger/10 text-danger',
  default: 'border-default bg-elevated text-secondary',
}

/**
 * SubscriptionCard — «Подписка для мобильных» on the «Входы в РФ» tab: the subscription domain
 * (go.<site>, A records on the reserve entries; its DNS and the Hub's certificate are checked live)
 * and the JSON «⚡ Авто» profile for Happ (off / the test user / everyone).
 * Props: data (GET /api/admin/entries), canEdit, onSaved().
 */
export default function SubscriptionCard({ data, canEdit, onSaved }) {
  const toast = useToast()
  const sub = data.subscription || {}
  const site = (data.entries.find(e => e.kind === 'hub')?.domain) || 'hiearth.ru'
  const [domain, setDomain] = useState(sub.domain || '')
  const [mode, setMode] = useState(sub.auto_mode || 'off')
  const [testUser, setTestUser] = useState(sub.auto_test_user || '')
  const [saving, setSaving] = useState('')
  useEffect(() => {
    setDomain(sub.domain || '')
    setMode(sub.auto_mode || 'off')
    setTestUser(sub.auto_test_user || '')
  }, [sub.domain, sub.auto_mode, sub.auto_test_user])

  const state = subscriptionDomainState(sub)
  const StateIcon = state.tone === 'success' ? CheckCircle2 : state.tone === 'default' ? Globe : TriangleAlert
  const relays = data.entries.filter(e => e.kind === 'relay' && e.lifecycle === 'online' && e.ip)
  const name = domain.trim() || sub.domain || suggestedSubDomain(site)
  const dns = new Set(sub.dns || [])

  async function save(patch, key, ok) {
    setSaving(key)
    try {
      await saveEntrySettings(patch)
      toast.success(ok)
      await onSaved()
    } catch (e) { toast.error(e.message) } finally { setSaving('') }
  }

  const modeChanged = mode !== (sub.auto_mode || 'off') || (mode === 'test' && testUser.trim() !== (sub.auto_test_user || ''))
  return (
    <Card id="subscription" className="scroll-mt-4">
      <CardHeader>
        <h3 className="flex items-center gap-2 text-sm font-semibold text-primary">
          <Zap className="w-4 h-4 text-accent" aria-hidden="true" /> Подписка для мобильных
        </h3>
      </CardHeader>
      <CardBody className="space-y-6">
        <section className="space-y-3" aria-labelledby="sub-domain-title">
          <h4 id="sub-domain-title" className="text-xs font-medium text-tertiary">1. Домен подписки — обновление через входы в РФ</h4>
          <p className="text-sm text-secondary">
            Сайт и кабинет остаются на {site}. Ссылка подписки и кабинет для телефонов открываются ещё и по поддомену, у которого
            A-записи указывают на входы в РФ: мобильная сеть режет адрес Hub, но не входы — подписка обновляется и в ней.
          </p>
          <div role="status" className={`flex items-start gap-2 rounded border px-3 py-2 text-sm ${TONE[state.tone]}`}>
            <StateIcon className="w-4 h-4 mt-0.5 shrink-0" aria-hidden="true" />
            <span className="min-w-0">{state.text}</span>
          </div>
          {relays.length > 0 && (
            <Table>
              <Thead><Tr><Th>Имя</Th><Th>Тип</Th><Th>Значение</Th><Th>TTL</Th><Th>Сейчас в DNS</Th></Tr></Thead>
              <Tbody>
                {relays.map(r => (
                  <Tr key={r.id}>
                    <Td mono>{name}</Td><Td mono>A</Td><Td mono>{r.ip}</Td><Td mono>300</Td>
                    <Td>{!sub.domain ? <span className="text-xs text-tertiary">—</span>
                      : dns.has(r.ip) ? <Badge variant="success">есть</Badge> : <Badge variant="warning">нет</Badge>}</Td>
                  </Tr>
                ))}
                {(sub.dns_foreign || []).map(ip => (
                  <Tr key={'x' + ip}>
                    <Td mono>{sub.domain}</Td><Td mono>A</Td><Td mono>{ip}</Td><Td mono>—</Td>
                    <Td><Badge variant="danger">удалить: не вход</Badge></Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          )}
          <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1 text-xs">
            <dt className="text-tertiary">Сертификат Hub для домена</dt>
            <dd className="text-primary">
              {!sub.domain ? '—' : sub.cert_ok
                ? <>действует до <span className="font-mono tabular-nums">{formatDate(sub.cert_until)}</span></>
                : <span className="text-danger">нет{sub.cert_error ? ` — ${sub.cert_error}` : ''}</span>}
            </dd>
            <dt className="text-tertiary">Ссылки подписки в кабинете ведут на</dt>
            <dd className="font-mono text-primary break-all">{sub.url || '—'}</dd>
          </dl>
          {canEdit && (
            <div className="flex flex-col sm:flex-row sm:items-end gap-2">
              <div className="w-full sm:w-72">
                <label htmlFor="sub-domain" className="text-xs text-tertiary block mb-1">Домен подписки (пусто — выключить)</label>
                <Input id="sub-domain" className="font-mono" placeholder={suggestedSubDomain(site)}
                  value={domain} onChange={e => setDomain(e.target.value)} />
              </div>
              <Button loading={saving === 'domain'} disabled={domain.trim() === (sub.domain || '')}
                onClick={() => save({ sub_domain: domain.trim() }, 'domain', domain.trim() ? 'Домен подписки сохранён — новые ссылки выдаются на нём' : 'Домен подписки выключен')}>
                Сохранить домен
              </Button>
            </div>
          )}
          <p className="text-xs text-tertiary">
            Порядок: A-записи у регистратора → <span className="font-mono">deploy --nginx</span> (добавит имя в сертификат) → «Сохранить домен».
            Без рабочего сертификата домен не сохранится. Уже добавленные подписки продолжают работать по старой ссылке.
          </p>
        </section>

        <section className="space-y-3" aria-labelledby="sub-auto-title">
          <h4 id="sub-auto-title" className="text-xs font-medium text-tertiary">2. «⚡ Авто» в Happ — сам выбирает самый быстрый вход</h4>
          <div role="radiogroup" aria-label="Режим «Авто»" className="grid grid-cols-1 lg:grid-cols-3 gap-2">
            {AUTO_MODES.map(m => (
              <label key={m.key} className={`flex gap-2 rounded border p-3 ${mode === m.key ? 'border-accent bg-accent/10' : 'border-default'} ${canEdit ? 'cursor-pointer hover:bg-hover' : 'opacity-50'}`}>
                <input type="radio" name="auto-mode" className="mt-1 accent-accent" checked={mode === m.key} disabled={!canEdit}
                  onChange={() => setMode(m.key)} />
                <span className="min-w-0">
                  <span className="block text-sm text-primary">{m.title}{(sub.auto_mode || 'off') === m.key && <Badge variant="accent" className="ml-2">сейчас</Badge>}</span>
                  <span className="block text-xs text-tertiary mt-1">{m.text}</span>
                </span>
              </label>
            ))}
          </div>
          {mode === 'test' && (
            <div className="w-full sm:w-72">
              <label htmlFor="auto-test-user" className="text-xs text-tertiary block mb-1">Тестовый пользователь «Авто»</label>
              <Input id="auto-test-user" className="font-mono" placeholder="ID из «Пользователей»" disabled={!canEdit}
                value={testUser} onChange={e => setTestUser(e.target.value)} />
            </div>
          )}
          {canEdit && (
            <Button loading={saving === 'auto'} disabled={!modeChanged || (mode === 'test' && !testUser.trim())}
              onClick={() => save(mode === 'test' ? { auto_mode: mode, auto_test_user: testUser.trim() } : { auto_mode: mode }, 'auto',
                'Режим «Авто» сохранён — подписки обновятся при следующем обновлении в Happ')}>
              Сохранить режим «Авто»
            </Button>
          )}
          <p className="text-xs text-tertiary">
            Только для Happ; остальные приложения и Clash получают прежний список. В Happ потяните подписку вниз («Обновить»), чтобы увидеть изменения сразу.
          </p>
        </section>
      </CardBody>
    </Card>
  )
}
