import { useState } from 'react'
import { Link } from 'react-router-dom'
import ConnectOrb from './ConnectOrb.jsx'
import Button from './Button.jsx'
import Alert from './Alert.jsx'
import Badge from './Badge.jsx'
import CopyField from './CopyField.jsx'
import QRDialog from './QRCode.jsx'
import { useToast } from './Toast.jsx'
import { primaryTelegramLink } from './telegramProxyModel.js'
import { happHref } from '../lib/account.js'
import { copyText } from '../lib/clipboard.js'
import { detectDevice, HAPP } from '../lib/apps.js'

// «Подключение» on the overview (spec 08 §8.1): the orb shows the subscription, «Добавить в Happ» is
// the screen's main action. Phone: centred orb, the button, then Telegram · QR-код · Ссылка (8.5).
export function ActiveConnect({ subLink, tg, phone, onHapp }) {
  const toast = useToast()
  const [qr, setQr] = useState(false)
  const [linkCopied, setLinkCopied] = useState(false)
  const url = subLink?.subscribe_url || ''
  const tgLink = primaryTelegramLink(tg, detectDevice())
  const tgProps = tgLink ? { href: tgLink.href, ...(tgLink.external ? { target: '_blank', rel: 'noopener noreferrer' } : {}) } : null

  const missing = !url && (
    <Alert tone="warn">Ссылку подписки сейчас не удалось получить — обновите страницу через минуту.</Alert>
  )

  async function copyLink() {
    if (await copyText(url)) {
      setLinkCopied(true)
      toast.ok('Ссылка скопирована')
      setTimeout(() => setLinkCopied(false), 2000)
    }
  }

  if (phone) {
    return (
      <section className="card card--glow stack gap-3" style={{ alignItems: 'center', textAlign: 'center', padding: 24 }} aria-labelledby="cc-t">
        <h2 id="cc-t" className="sr">Подключение</h2>
        <ConnectOrb active label="Готово" style={{ width: 160, height: 160 }} to={url ? happHref(url) : '/connect'} onClick={url ? onHapp : undefined} ariaLabel={url ? 'Подписка активна — добавить в Happ' : 'Подписка активна — открыть подключение'} />
        <p className="muted t-sm">Выход — Нидерланды</p>
        {missing || (
          <>
            <Button variant="primary" size="lg" block to={happHref(url)} onClick={onHapp}>Добавить в Happ</Button>
            <div className="row gap-2" style={{ width: '100%' }}>
              {tgProps && <Button variant="secondary" {...tgProps} style={{ flex: 1, paddingInline: 8 }}>Telegram</Button>}
              <Button variant="secondary" onClick={() => setQr(true)} style={{ flex: 1, paddingInline: 8 }}>QR-код</Button>
              <Button variant="secondary" onClick={copyLink} style={{ flex: 1, paddingInline: 8 }} aria-live="polite">{linkCopied ? '✓' : 'Ссылка'}</Button>
            </div>
          </>
        )}
        <QRDialog open={qr} onClose={() => setQr(false)} data={url} />
      </section>
    )
  }

  return (
    <section className="card card--glow" style={{ display: 'grid', gridTemplateColumns: '200px minmax(0, 1fr)', gap: 28, alignItems: 'center', padding: 28 }} aria-labelledby="cc-t">
      <ConnectOrb active label="Готово" to={url ? happHref(url) : '/connect'} onClick={url ? onHapp : undefined} ariaLabel={url ? 'Подписка активна — добавить в Happ' : 'Подписка активна — открыть подключение'} />
      <div className="stack gap-3" style={{ minWidth: 0 }}>
        <div>
          <h2 id="cc-t" className="t-h2">Подключение</h2>
          <p className="muted t-sm" style={{ marginTop: 4 }}>Выход — Нидерланды. Путь выбирается автоматически.</p>
        </div>
        {missing || (
          <>
            <Button variant="primary" size="lg" iconRight="arrowRight" to={happHref(url)} onClick={onHapp}>Добавить в Happ</Button>
            <div className="row gap-2">
              {tgProps && <Button variant="secondary" icon="send" {...tgProps} style={{ flex: 1 }}>Открыть в Telegram</Button>}
              <Button variant="secondary" icon="qr" aria-label="Показать QR-код" onClick={() => setQr(true)} />
            </div>
            {tgLink?.fallback && <p className="t-xs subtle" style={{ marginTop: -4 }}>Telegram не открылся? <a className="link" href={tgLink.fallback} {...(tgLink.fallback.startsWith('http') ? { target: '_blank', rel: 'noopener noreferrer' } : {})}>{tgLink.fallback.startsWith('http') ? 'Открыть через t.me' : 'Открыть в приложении'}</a></p>}
            <CopyField value={url} label="Ссылка подписки" />
            <Link to="/connect#apps" className="t-sm link">Другие приложения: v2rayNG, Streisand…</Link>
          </>
        )}
      </div>
      <QRDialog open={qr} onClose={() => setQr(false)} data={url} />
    </section>
  )
}

// No subscription: the button will appear here; the app can be installed ahead.
export function WaitingConnect() {
  return (
    <section className="card">
      <div className="card__head"><h2 className="card__title">Подключение</h2></div>
      <Alert tone="plain" icon="lock">Кнопка «Добавить в Happ» появится здесь, как только включится подписка. Пока можно установить приложение заранее.</Alert>
      <div className="row wrap gap-2" style={{ marginTop: 16 }}>
        <Button variant="secondary" href={HAPP.ios.url} target="_blank" rel="noopener noreferrer">{HAPP.ios.label}</Button>
        <Button variant="secondary" href={HAPP.android.url} target="_blank" rel="noopener noreferrer">{HAPP.android.label}</Button>
        <Button variant="secondary" href={HAPP.desktop.url} target="_blank" rel="noopener noreferrer">Windows и macOS</Button>
      </div>
    </section>
  )
}

// Expired: the subscription stays in Happ; after paying it is refreshed, not added again.
export function PausedConnect({ phone }) {
  return (
    <section className="card" style={phone ? undefined : { display: 'grid', gridTemplateColumns: '140px minmax(0, 1fr) auto', gap: 24, alignItems: 'center' }}>
      {!phone && <ConnectOrb active={false} small to="/payment" ariaLabel="Подключение на паузе — продлить подписку" />}
      <div>
        <h2 className="t-h3">Подключение на паузе</h2>
        <p className="muted t-sm" style={{ marginTop: 4 }}>Подписка в Happ остаётся. После оплаты потяните список вниз в Happ, чтобы обновить её, — добавлять заново не нужно.</p>
      </div>
      <Badge style={phone ? { marginTop: 12 } : undefined}>На паузе</Badge>
    </section>
  )
}
