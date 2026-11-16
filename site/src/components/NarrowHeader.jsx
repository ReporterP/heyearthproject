import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount from '../account/useAccount.js'
import Icon from './Icon.jsx'
import Logo from './Logo.jsx'
import { IdChip } from './Nomerok.jsx'

// The narrow header of nested pages and the payment path (02 §30, 09): `back` on the left,
// the logo or the steps in the middle, the ID chip (or the wallet) on the right.
//   <NarrowHeader back={{ to: '/dashboard', label: 'В кабинет' }} center={<Steps…/>} right={<WalletPill/>} />
export function Back({ to, label, onClick }) {
  const body = <><Icon name="back" size={16} strokeWidth={2.2} /><span style={{ whiteSpace: 'nowrap' }}>{label}</span></>
  if (onClick) return <button type="button" className="back" onClick={onClick} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer' }}>{body}</button>
  return <Link to={to} className="back">{body}</Link>
}

export default function NarrowHeader({ back, center, right, logoText }) {
  const { isLoggedIn } = useAuth()
  const { data } = useAccount(isLoggedIn && right === undefined ? ['profile'] : [], { optional: ['profile'] })
  const id = data?.profile?.user_id
  return (
    <header className="site-nav">
      <div className="container site-nav__in">
        <div style={{ flex: '1 1 auto' }}>{back ? <Back {...back} /> : <Logo text={logoText} />}</div>
        <div className="row" style={{ flex: '0 1 auto', justifyContent: 'center' }}>{center ?? (back ? <Logo text={logoText} /> : null)}</div>
        <div className="row" style={{ flex: '1 1 0', justifyContent: 'flex-end' }}>
          {right !== undefined ? right : id ? <span className="max-sm:hidden"><IdChip id={id} /></span> : null}
        </div>
      </div>
    </header>
  )
}
