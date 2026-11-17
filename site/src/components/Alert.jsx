import Icon from './Icon.jsx'

// Alert (spec 02 §13): a message built into the page. The first phrase is bold (what happened),
// then what to do. `bad` is role=alert, the rest role=status. No close cross.
//   <Alert tone="info|ok|warn|bad" title="Тарифы недоступны.">Не удалось загрузить список. <button…>Обновить</button></Alert>
const ICON = { info: 'info', ok: 'ok', warn: 'warn', bad: 'bad', plain: 'lock' }

export default function Alert({ tone = 'info', title, children, icon, className = '', rise = false, style }) {
  return (
    <div
      className={`alert ${tone === 'plain' ? '' : `alert--${tone}`} ${rise ? 'rise' : ''} ${className}`}
      role={tone === 'bad' ? 'alert' : 'status'}
      style={rise ? { '--d': '0ms', animationDuration: 'var(--dur-3)', ...style } : style}
    >
      <Icon name={icon || ICON[tone] || 'info'} />
      <span>
        {title && <b>{title}</b>}{title && children ? ' ' : null}{children}
      </span>
    </div>
  )
}
