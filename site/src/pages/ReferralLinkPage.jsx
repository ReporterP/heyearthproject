import { useEffect } from 'react'
import { Navigate, useParams } from 'react-router-dom'
import { rememberReferral } from '../lib/referral.js'
import { useAuth } from '../auth/AuthContext.jsx'

// /r/<code>: remember the inviter's code and go on to the registration (a signed-in
// visitor goes to the cabinet, where the «Код друга» field takes it).
export default function ReferralLinkPage() {
  const { code } = useParams()
  const { isLoggedIn } = useAuth()
  useEffect(() => { rememberReferral(code) }, [code])
  return <Navigate to={isLoggedIn ? '/dashboard' : '/register'} replace />
}
