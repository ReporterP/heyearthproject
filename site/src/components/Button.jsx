import { forwardRef } from 'react'
import { Link } from 'react-router-dom'
import Icon from './Icon.jsx'

// Button (spec 02 §1). One primary per screen (plus one in an open dialog).
//   <Button variant="primary|secondary|ghost|danger" size="sm|md|lg" block loading disabled
//           icon="copy" iconRight="arrowRight" to="/path" | href="…" | onClick>
// A `to` renders a router Link, an `href` an <a>, otherwise a <button>. While `loading` the
// label turns transparent behind a spinner, the width stays, clicks are ignored, aria-busy is set.
const Button = forwardRef(function Button(
  { variant = 'secondary', size, block, loading, disabled, icon, iconRight, to, href, type = 'button', className = '', children, ...rest },
  ref,
) {
  const cls = [
    'btn',
    `btn--${variant}`,
    size === 'sm' ? 'btn--sm' : size === 'lg' ? 'btn--lg' : '',
    block ? 'btn--block' : '',
    !children && icon ? 'btn--icon' : '',
    loading ? 'is-loading' : '',
    disabled && (to || href) ? 'is-disabled' : '',
    className,
  ].filter(Boolean).join(' ')
  const body = (
    <>
      {icon ? <Icon name={icon} /> : null}
      {children}
      {iconRight ? <Icon name={iconRight} /> : null}
    </>
  )
  if (to && !disabled) {
    return <Link ref={ref} to={to} className={cls} aria-busy={loading || undefined} {...rest}>{body}</Link>
  }
  if (href && !disabled) {
    return <a ref={ref} href={href} className={cls} aria-busy={loading || undefined} {...rest}>{body}</a>
  }
  if (to || href) {
    return <span ref={ref} className={cls} aria-disabled="true" {...rest}>{body}</span>
  }
  return (
    <button ref={ref} type={type} className={cls} disabled={disabled} aria-busy={loading || undefined} {...rest}
      onClick={loading ? (e) => e.preventDefault() : rest.onClick}>
      {body}
    </button>
  )
})

export default Button
