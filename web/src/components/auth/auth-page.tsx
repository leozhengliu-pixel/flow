import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { ArrowLeft, Check, Eye, EyeOff, LoaderCircle, Mail } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'

import {
  acceptInvitation,
  fetchInvitationPreview,
  forgotPassword,
  loginAccount,
  registerAccount,
  resendVerification,
  resetPassword,
  verifyEmail,
} from '@/lib/api'
import type { AuthSession, InvitationPreview } from '@/types/flow'
import { LanguageSelect } from '@/i18n/i18n'

import './auth-page.css'

type Props = {
  session: AuthSession | null
  onAuthenticated: (session: AuthSession, returnTo?: string) => Promise<void>
  onInvitationAccepted: (workspaceKey: string) => Promise<void>
}

export function AuthPage({ session, onAuthenticated, onInvitationAccepted }: Props) {
  const location = useLocation()
  const navigate = useNavigate()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const route = location.pathname.split('/').filter(Boolean)
  const mode = route[0] || 'login'
  const inviteToken = mode === 'invite' ? route[1] ?? '' : ''
  const returnTo = params.get('returnTo') || undefined
  const [email, setEmail] = useState(params.get('email') ?? '')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [devToken, setDevToken] = useState('')
  const [invitation, setInvitation] = useState<InvitationPreview | null>(null)

  useEffect(() => {
    if (!inviteToken) return
    setPending(true)
    fetchInvitationPreview(inviteToken)
      .then(setInvitation)
      .catch(error => setError(error instanceof Error ? error.message : 'This invitation is no longer valid.'))
      .finally(() => setPending(false))
  }, [inviteToken])

  const run = async (action: () => Promise<void>) => {
    setPending(true)
    setError('')
    try { await action() }
    catch (error) { setError(error instanceof Error ? error.message : 'Something went wrong.') }
    finally { setPending(false) }
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const submittedName = String(form.get('name') ?? name).trim()
    const submittedEmail = String(form.get('email') ?? email).trim()
    const submittedPassword = String(form.get('password') ?? password)
    setName(submittedName)
    setEmail(submittedEmail)
    setPassword(submittedPassword)
    if (mode === 'login') void run(async () => {
      const authenticated = await loginAccount(submittedEmail, submittedPassword)
      await onAuthenticated(authenticated, returnTo)
    })
    if (mode === 'signup') void run(async () => {
      const result = await registerAccount({ name: submittedName, email: submittedEmail, password: submittedPassword })
      const next = new URLSearchParams({ email: submittedEmail, ...(returnTo ? { returnTo } : {}) })
      if (result.verificationToken) next.set('token', result.verificationToken)
      navigate(`/verify-email?${next}`)
    })
    if (mode === 'forgot-password') void run(async () => {
      const result = await forgotPassword(submittedEmail)
      setMessage('Check your email for a password reset link.')
      setDevToken(result.resetToken ?? '')
    })
    if (mode === 'reset-password') void run(async () => {
      const token = params.get('token') ?? ''
      await resetPassword(token, submittedPassword)
      setMessage('Your password has been updated.')
    })
  }

  if (mode === 'invite') {
    return <AuthShell>
      <div className="auth-invite-mark">{initials(invitation?.workspace.name ?? '')}</div>
      <h1>{invitation ? `Join ${invitation.workspace.name}` : 'Workspace invitation'}</h1>
      {pending && !invitation ? <LoaderCircle className="auth-spinner"/> : invitation && <>
        <p><strong>{invitation.email}</strong> was invited as {invitation.role === 'guest' ? 'a guest' : invitation.role === 'admin' ? 'an admin' : 'a member'}.</p>
        {session ? <button className="auth-primary" disabled={pending || session.user.email.toLowerCase() !== invitation.email.toLowerCase()} onClick={() => void run(async () => {
          await acceptInvitation(inviteToken)
          await onInvitationAccepted(invitation.workspace.urlKey)
        })}>{pending ? <LoaderCircle className="auth-spinner"/> : 'Join workspace'}</button> : <div className="auth-invite-actions">
          <button className="auth-primary" onClick={() => navigate(`/signup?email=${encodeURIComponent(invitation.email)}&returnTo=${encodeURIComponent(location.pathname)}`)}>Create account</button>
          <button className="auth-secondary" onClick={() => navigate(`/login?email=${encodeURIComponent(invitation.email)}&returnTo=${encodeURIComponent(location.pathname)}`)}>Sign in</button>
        </div>}
        {session && session.user.email.toLowerCase() !== invitation.email.toLowerCase() && <div className="auth-error">Sign in as {invitation.email} to accept this invitation.</div>}
      </>}
      {error && <div className="auth-error">{error}</div>}
    </AuthShell>
  }

  if (mode === 'verify-email') {
    const token = params.get('token') ?? ''
    return <AuthShell>
      <div className="auth-mail-icon"><Mail/></div>
      <h1>Verify your email</h1>
      <p>We sent a verification link to <strong>{params.get('email')}</strong>.</p>
      {token && !message && <button className="auth-primary" disabled={pending} onClick={() => void run(async () => { await verifyEmail(token); setMessage('Email verified. You can now sign in.') })}>{pending ? <LoaderCircle className="auth-spinner"/> : 'Verify email'}</button>}
      {message && <div className="auth-success"><Check/>{message}</div>}
      <button className="auth-text-button" onClick={() => navigate(`/login?email=${encodeURIComponent(params.get('email') ?? '')}${returnTo ? `&returnTo=${encodeURIComponent(returnTo)}` : ''}`)}>Continue to login</button>
      <button className="auth-text-button" disabled={pending} onClick={() => void run(async () => { const result = await resendVerification(params.get('email') ?? ''); if (result.verificationToken) navigate(`/verify-email?email=${encodeURIComponent(params.get('email') ?? '')}&token=${encodeURIComponent(result.verificationToken)}`); else setMessage('A new verification email has been sent.'); })}>Resend verification email</button>
      {error && <div className="auth-error">{error}</div>}
    </AuthShell>
  }

  const isSignup = mode === 'signup'
  const isForgot = mode === 'forgot-password'
  const isReset = mode === 'reset-password'
  return <AuthShell>
    {(isForgot || isReset) && <button className="auth-back" onClick={() => navigate('/login')}><ArrowLeft/>Back</button>}
    <h1>{isSignup ? 'Create your account' : isForgot ? 'Reset your password' : isReset ? 'Choose a new password' : 'Log in to Flow'}</h1>
    {!isReset && !isForgot && <button className="auth-google" type="button" onClick={() => setError('Google sign-in is not configured for this workspace.')}><span>G</span>Continue with Google</button>}
    {!isReset && !isForgot && <div className="auth-divider"><span>or</span></div>}
    <form onSubmit={submit}>
      {isSignup && <label>Full name<input name="name" autoFocus autoComplete="name" value={name} onChange={event => setName(event.target.value)} placeholder="Your name" required/></label>}
      {!isReset && <label>Email address<input name="email" autoFocus={!isSignup} type="email" autoComplete="email" value={email} onChange={event => setEmail(event.target.value)} placeholder="name@company.com" required/></label>}
      {!isForgot && <label>Password<div className="auth-password"><input name="password" autoFocus={isReset} type={showPassword ? 'text' : 'password'} autoComplete={isSignup ? 'new-password' : 'current-password'} value={password} onChange={event => setPassword(event.target.value)} placeholder={isSignup || isReset ? 'At least 8 characters' : 'Enter your password'} minLength={8} required/><button type="button" aria-label={showPassword ? 'Hide password' : 'Show password'} onClick={() => setShowPassword(value => !value)}>{showPassword ? <EyeOff/> : <Eye/>}</button></div></label>}
      {!isSignup && !isForgot && !isReset && <button type="button" className="auth-forgot" onClick={() => navigate(`/forgot-password?email=${encodeURIComponent(email)}`)}>Forgot password?</button>}
      <button className="auth-primary" disabled={pending}>{pending ? <LoaderCircle className="auth-spinner"/> : isSignup ? 'Create account' : isForgot ? 'Send reset link' : isReset ? 'Update password' : 'Continue'}</button>
    </form>
    {message && <div className="auth-success"><Check/>{message}</div>}
    {devToken && <button className="auth-dev-link" onClick={() => navigate(`/reset-password?token=${encodeURIComponent(devToken)}`)}>Open development reset link</button>}
    {error && <div className="auth-error">{error}</div>}
    {!isForgot && !isReset && <p className="auth-switch">{isSignup ? 'Already have an account?' : 'New to Flow?'} <button onClick={() => navigate(isSignup ? '/login' : '/signup')}>{isSignup ? 'Log in' : 'Create an account'}</button></p>}
  </AuthShell>
}

function AuthShell({ children }: { children: React.ReactNode }) {
  return <main className="auth-page"><LanguageSelect className="auth-language"/><div className="auth-brand"><span className="auth-brand-mark"/>Flow</div><section className="auth-panel">{children}</section><footer>Privacy&nbsp;&nbsp;·&nbsp;&nbsp;Terms</footer></main>
}

function initials(value: string) { return value.split(/\s+/).map(part => part[0]).join('').slice(0, 2).toUpperCase() || 'L' }
