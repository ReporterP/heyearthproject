import { useEffect, useState } from 'react'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount from '../account/useAccount.js'
import AppShell, { PageHead } from '../components/AppShell.jsx'
import Alert from '../components/Alert.jsx'
import AppDownloads from '../components/AppDownloads.jsx'
import Button from '../components/Button.jsx'
import ConnectOrb from '../components/ConnectOrb.jsx'
import CopyField from '../components/CopyField.jsx'
import EmptyState from '../components/EmptyState.jsx'
import QRDialog from '../components/QRCode.jsx'
import TelegramCard from '../components/TelegramCard.jsx'
import { Segmented } from '../components/Controls.jsx'
import { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import useMtproto from '../hooks/useMtproto.js'
import useMedia, { PHONE } from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { happHref } from '../lib/account.js'
import { detectDevice, OTHER_APPS } from '../lib/apps.js'

// Подключение /connect (spec 08 §8.2): composed from the overview's «Подключение» card and the
// «Подключение» artboard parts. A 503 on /user/connections is «временно недоступно», never «нет подписки».
// subscription_version: the server bumps it whenever users' subscription lines change (entries,
// exits, entry mode). config_version is no longer written by anything (QA 2026-11-18).
const VERSION_KEY = 'heyearth_subscription_version'

const GUIDE = {
  ios: [
    <><b>Установите Happ</b> из App Store.</>,
    <><b>Нажмите «Добавить в Happ»</b> выше — приложение откроется само.</>,
    <>Если не открылось: <b>скопируйте ссылку</b> и в Happ нажмите Подписки → + → Вставить из буфера.</>,
    <><b>Включите подключение</b> переключателем в приложении.</>,
  ],
  android: [
    <><b>Скачайте Happ</b> с сайта happ.info — файл APK (в Google Play его нет).</>,
    <><b>Нажмите «Добавить в Happ»</b> выше — приложение откроется само.</>,
    <>Если не открылось: <b>скопируйте ссылку</b>, в приложении → + → Импорт из буфера обмена.</>,
    <><b>Включите подключение</b> переключателем.</>,
  ],
  desktop: [
    <><b>Скачайте Happ</b> для Windows, macOS или Linux.</>,
    <><b>Скопируйте ссылку подписки</b> кнопкой «Копировать» выше.</>,
    <>В приложении: <b>Подписки → Добавить</b> → вставьте ссылку.</>,
    <><b>Включите подключение.</b></>,
  ],
}

export default function ConnectionsPage() {
  const { token } = useAuth()
  const phone = useMedia(PHONE)
  usePageMeta('Подключение — HiEarth', { noindex: true })
  const { data, loading, error, reload } = useAccount(['connections', 'subLink'], { optional: ['subLink'] })
  const conn = data.connections
  const url = data.subLink?.subscribe_url || ''
  const canConnect = !!conn?.can_connect
  const tg = useMtproto(token, canConnect)
  const [device, setDevice] = useState(detectDevice)
  const [updated, setUpdated] = useState(false)
  const [qr, setQr] = useState(false)
  const showSkel = useDelayed(loading && !conn, 300)

  // «Настройки обновились»: subscription_version changed since this browser last acknowledged it
  useEffect(() => {
    const v = conn?.subscription_version
    if (v === undefined || v === null) return
    let saved = null
    try { saved = localStorage.getItem(VERSION_KEY) } catch { /* storage off */ }
    if (!saved) { try { localStorage.setItem(VERSION_KEY, String(v)) } catch { /* storage off */ } return }
    if (saved !== String(v)) setUpdated(true)
  }, [conn?.subscription_version])
  const acknowledge = () => {
    try { localStorage.setItem(VERSION_KEY, String(conn?.subscription_version)) } catch { /* storage off */ }
    setUpdated(false)
  }

  const guide = (
    <section id="guide" className="card">
      <div className="card__head" style={{ flexWrap: 'wrap' }}>
        <h2 className="t-h3">Пошаговая инструкция</h2>
        <Segmented label="Устройство" value={device} onChange={setDevice} options={[{ value: 'ios', label: 'iPhone' }, { value: 'android', label: 'Android' }, { value: 'desktop', label: 'Компьютер' }]} />
      </div>
      <ol key={device} className="num swap">{GUIDE[device].map((s, i) => <li key={i}><span>{s}</span></li>)}</ol>
    </section>
  )
  const apps = (
    <section id="apps" className="card">
      <h2 className="t-h3" style={{ marginBottom: 16 }}>Приложения</h2>
      <AppDownloads />
      <p className="t-sm subtle" style={{ marginTop: 16 }}>
        Другие приложения:{' '}
        {OTHER_APPS.map((a, i) => <span key={a.name}>{i ? ', ' : ''}<a href={a.url} className="link" target="_blank" rel="noopener noreferrer">{a.name}</a> ({a.platforms})</span>)}
        {' '}— принимают ту же ссылку подписки.
      </p>
    </section>
  )

  let body
  if (error && error.kind !== 'auth') {
    body = <EmptyState mood="worried" title="Не удалось загрузить данные" actions={<Button variant="secondary" onClick={() => reload()}>Повторить</Button>}>Обновите страницу.</EmptyState>
  } else if (!conn) {
    body = <div className="stack gap-4" aria-busy="true">{showSkel && [0, 1, 2].map((i) => <SkeletonCard key={i} lines={3} />)}</div>
  } else if (!canConnect) {
    body = (
      <div className="stack gap-4">
        <section className="card">
          <EmptyState mood="search" title="Нет активной подписки" actions={<><Button variant="primary" to="/payment">Выбрать тариф</Button><Button variant="ghost" to="/dashboard">В кабинет</Button></>}>
            Оформите подписку или активируйте промокод — и данные для подключения появятся здесь.
          </EmptyState>
        </section>
        {guide}
        {apps}
      </div>
    )
  } else {
    body = (
      <div className="stack gap-4">
        {updated && (
          <Alert tone="info" title="Настройки обновились.">
            Параметры подключения оптимизированы. Обновите подписку в приложении, чтобы применить их.{' '}
            <Button variant="ghost" size="sm" onClick={acknowledge} style={{ marginLeft: 4 }}>Понятно</Button>
          </Alert>
        )}
        <section className="card card--glow rise" style={phone ? { padding: 24, textAlign: 'center' } : { display: 'grid', gridTemplateColumns: '120px minmax(0,1fr)', gap: 24, alignItems: 'center', padding: 28 }}>
          <ConnectOrb active small to={url ? happHref(url) : undefined} ariaLabel={url ? 'Подписка активна — добавить в Happ' : 'Подписка активна'} style={phone ? { margin: '0 auto 16px' } : undefined} />
          <div className="stack gap-3">
            <div>
              <h2 className="t-h2">Подключить в приложении</h2>
              <p className="muted t-sm" style={{ marginTop: 4 }}>Откроет приложение и добавит подписку — ничего копировать не нужно.</p>
            </div>
            {url
              ? <Button variant="primary" size="lg" block={phone} iconRight="arrowRight" to={happHref(url)} style={phone ? undefined : { alignSelf: 'flex-start' }}>Добавить в Happ</Button>
              : <Alert tone="warn">Ссылку подписки сейчас не удалось получить — обновите страницу через минуту.</Alert>}
            <p className="t-sm muted">Нет приложения? <a href="#apps" className="link" onClick={(e) => { e.preventDefault(); document.getElementById('apps')?.scrollIntoView({ block: 'start' }) }}>Скачайте Happ</a> — займёт минуту</p>
          </div>
        </section>

        <TelegramCard vm={tg} />

        {url && (
          <section className="card">
            <h2 className="t-h3">Или добавить вручную</h2>
            <p className="muted t-sm" style={{ marginTop: 4 }}>Отсканируйте QR-код в приложении или скопируйте ссылку подписки.</p>
            <div className="stack gap-3" style={{ marginTop: 16 }}>
              <CopyField value={url} label="Ссылка подписки" expandable secret />
              <div><Button variant="ghost" size="sm" icon="qr" onClick={() => setQr(true)}>Показать QR-код</Button></div>
              <p className="t-xs subtle">Работает в Happ и других приложениях, которые принимают ссылку подписки. Настройки обновляются сами.</p>
            </div>
            <QRDialog open={qr} onClose={() => setQr(false)} data={url} />
          </section>
        )}
        {guide}
        {apps}
      </div>
    )
  }

  return (
    <AppShell>
      <div style={{ maxWidth: 860 }}>
        <PageHead title="Подключение" lead="Настройте устройство за пару минут" />
        {body}
      </div>
    </AppShell>
  )
}
