import { useState } from 'react'
import Button from './Button.jsx'
import Icon from './Icon.jsx'
import { useToast } from './Toast.jsx'
import { copyText } from '../lib/clipboard.js'

// «Прокси для Telegram» on /connect (spec 08 §8.2 item 4; phase T): the WEB proxy first (Telegram
// Desktop 7.1+ and Android) with its manual address+secret, then the classic proxy (iPhone/iPad and
// older Telegram). Fed by the view model of telegramProxyModel.js; renders nothing without it.
// The WEB address is a host name only — never shown as a «server:port» pair (plan T4).
const ext = { target: '_blank', rel: 'noopener noreferrer' }

function WebSection({ web }) {
  const toast = useToast()
  const [copied, setCopied] = useState(false)
  const main = web.link ? { href: web.link, ...ext } : { href: web.tgLink }
  async function copy() {
    if (await copyText(web.copyText)) {
      setCopied(true)
      toast.ok('Адрес и секрет скопированы')
      setTimeout(() => setCopied(false), 2000)
    } else {
      toast.bad('Браузер не дал скопировать — выделите адрес и секрет вручную')
    }
  }
  return (
    <div className="card card--elevated" style={{ padding: 20 }}>
      <h3 className="t-h4">WEB-прокси</h3>
      <p className="t-sm subtle" style={{ marginTop: 2 }}>Telegram Desktop 7.1+ и Android — обновите Telegram до последней версии.</p>
      <div className="row wrap gap-3" style={{ marginTop: 14 }}>
        <Button variant="secondary" icon="send" {...main}>Подключить</Button>
        {web.link && web.tgLink && <a href={web.tgLink} className="t-sm link">Открыть в приложении</a>}
      </div>
      {web.copyText && (
        <div style={{ marginTop: 16, paddingTop: 16, borderTop: '1px solid var(--line)' }}>
          <dl className="kv" style={{ gridTemplateColumns: '80px minmax(0, 1fr)', fontSize: 14 }}>
            <dt>Адрес</dt><dd className="mono" style={{ wordBreak: 'break-all', userSelect: 'all' }}>{web.server}</dd>
            <dt>Секрет</dt><dd className="mono muted" style={{ wordBreak: 'break-all', userSelect: 'all' }}>{web.secret}</dd>
          </dl>
          <Button variant="ghost" size="sm" icon={copied ? 'check' : 'copy'} onClick={copy} style={{ marginTop: 10 }} aria-live="polite">
            {copied ? '✓ Скопировано' : 'Скопировать адрес и секрет'}
          </Button>
          <p className="t-xs subtle" style={{ marginTop: 8 }}>
            Если кнопка не сработала: Telegram → Настройки → Данные и память → Прокси → Добавить прокси → тип «WEB-прокси», вставьте адрес и секрет.
          </p>
        </div>
      )}
    </div>
  )
}

function ClassicSection({ classic }) {
  return (
    <div className="card card--elevated" style={{ padding: 20 }}>
      <h3 className="t-h4">Классический прокси</h3>
      <p className="t-sm subtle" style={{ marginTop: 2 }}>Для iPhone и iPad и старых версий Telegram.</p>
      <div className="row wrap gap-2" style={{ marginTop: 14 }}>
        {classic.tgLink && <Button variant="secondary" icon="send" href={classic.tgLink}>Открыть в Telegram</Button>}
        {classic.link && <Button variant="ghost" href={classic.link} {...ext}>Открыть через t.me</Button>}
      </div>
    </div>
  )
}

export default function TelegramCard({ vm }) {
  if (!vm) return null
  return (
    <section className="card" aria-labelledby="tg-t">
      <div className="row gap-3" style={{ alignItems: 'flex-start', marginBottom: 16 }}>
        <span className="rowi__ic" aria-hidden="true"><Icon name="send" /></span>
        <div>
          <h2 id="tg-t" className="t-h3">Прокси для Telegram</h2>
          <p className="t-sm muted" style={{ marginTop: 2 }}>Отдельный канал только для мессенджера, помимо основного подключения.</p>
        </div>
      </div>
      <div className="stack gap-3">
        {vm.web && <WebSection web={vm.web} />}
        {vm.classic && <ClassicSection classic={vm.classic} />}
      </div>
    </section>
  )
}
