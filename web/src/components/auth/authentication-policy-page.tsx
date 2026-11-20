import {useState} from 'react'
import {ShieldCheck} from 'lucide-react'
import {useI18n} from '@/i18n/i18n'
import {beginPasskeyRegistration,finishPasskeyRegistration,logoutAccount,verifyMfaWithPasskey} from '@/lib/api'
import {ApiError} from '@/lib/api-client'
import {toCredentialCreationOptions,serializeCreationCredential} from '@/lib/webauthn'

export function AuthenticationPolicyPage({code}:{code:string}) {
  const {t}=useI18n()
  const [busy,setBusy]=useState(false)
  const [error,setError]=useState('')
  const [enroll,setEnroll]=useState(false)
  const verify=async()=>{
    setBusy(true);setError('')
    try {
      if(enroll) {
        const start=await beginPasskeyRegistration()
        const credential=await navigator.credentials.create({publicKey:toCredentialCreationOptions(start.options)}) as PublicKeyCredential|null
        if(!credential)throw new Error(t('Passkey registration was canceled'))
        await finishPasskeyRegistration({registrationId:start.registrationId,name:'Two-factor passkey',credential:serializeCreationCredential(credential,credential.response as AuthenticatorAttestationResponse)})
        setEnroll(false)
      }
      await verifyMfaWithPasskey(t('Verification was canceled'))
      location.reload()
    } catch(reason) {
      if(reason instanceof ApiError&&reason.code==='mfa_enrollment_required') {setEnroll(true);setError(t('Register a passkey, then verify it to access this workspace'))}
      else setError(reason instanceof Error?reason.message:t('Could not verify authentication'))
    } finally {setBusy(false)}
  }
  return <main className="auth-page"><div className="auth-brand">Flow</div><div className="auth-form"><ShieldCheck size={24}/><h1>{t(code==='mfa_required'?'Verify your identity':'Authentication method required')}</h1><p>{t(code==='mfa_required'?'This workspace requires a second authentication factor.':'Sign in using a method allowed by your workspace administrator.')}</p>{error&&<p role="alert">{error}</p>}{code==='mfa_required'&&<button type="button" disabled={busy} onClick={()=>void verify()}>{t(enroll?'Register passkey':'Verify with passkey')}</button>}<button type="button" disabled={busy} onClick={async()=>{await logoutAccount();location.assign('/login')}}>{t('Sign in with another method')}</button></div></main>
}
