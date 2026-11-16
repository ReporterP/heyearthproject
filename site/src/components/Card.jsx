// Card (spec 02 §10). Variants: flat, elevated, glow (one per screen), link (whole card is a link).
//   <Card variant="glow" title="Кошелёк" action={<Link…>История</Link>}>…</Card>
export default function Card({ variant, title, action, as: Tag = 'section', className = '', style, children, ...rest }) {
  const cls = ['card', variant ? `card--${variant}` : '', className].filter(Boolean).join(' ')
  return (
    <Tag className={cls} style={style} {...rest}>
      {(title || action) && (
        <div className="card__head">
          {title ? <h2 className="card__title">{title}</h2> : <span />}
          {action}
        </div>
      )}
      {children}
    </Tag>
  )
}
