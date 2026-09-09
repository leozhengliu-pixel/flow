import {useState} from 'react'
import {ShieldCheck} from 'lucide-react'
import {useI18n} from '@/i18n/i18n'
import {beginPasskeyRegistration,finishPasskeyRegistration,logoutAccount} from '@/lib/api'
import {ApiError,jsonRequest,request} from '@/lib/api-client'
import {decodeBase64Url,encodeBase64Url,toCredentialCreationOptions,serializeCreationCredential} from '@/lib/webauthn'

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
      const start=await request<{challengeId:string;options:{publicKey:PublicKeyCredentialRequestOptions & {challenge:string;allowCredentials?:{id:string;type:'public-key'}[]}}}>('/api/account/mfa/start',{method:'POST'})
      const source=start.options.publicKey
      const credential=await navigator.credentials.get({publicKey:{...source,challenge:decodeBase64Url(String(source.challenge)),allowCredentials:source.allowCredentials?.map(item=>({...item,id:decodeBase64Url(String(item.id))}))}}) as PublicKeyCredential|null
      if(!credential)throw new Error(t('Verification was canceled'))
      const response=credential.response as AuthenticatorAssertionResponse
      await request('/api/account/mfa/finish',jsonRequest('POST',{challengeId:start.challengeId,credential:{id:credential.id,type:credential.type,rawId:encodeBase64Url(credential.rawId),response:{clientDataJSON:encodeBase64Url(response.clientDataJSON),authenticatorData:encodeBase64Url(response.authenticatorData),signature:encodeBase64Url(response.signature),userHandle:response.userHandle?encodeBase64Url(response.userHandle):null}}}))
      location.reload()
    } catch(reason) {
      if(reason instanceof ApiError&&reason.code==='mfa_enrollment_required') {setEnroll(true);setError(t('Register a passkey, then verify it to access this workspace'))}
      else setError(reason instanceof Error?reason.message:t('Could not verify authentication'))
    } finally {setBusy(false)}
  }
  return <main className="auth-page"><div className="auth-brand">Flow</div><div className="auth-form"><ShieldCheck size={24}/><h1>{t(code==='mfa_required'?'Verify your identity':'Authentication method required')}</h1><p>{t(code==='mfa_required'?'This workspace requires a second authentication factor.':'Sign in using a method allowed by your workspace administrator.')}</p>{error&&<p role="alert">{error}</p>}{code==='mfa_required'&&<button type="button" disabled={busy} onClick={()=>void verify()}>{t(enroll?'Register passkey':'Verify with passkey')}</button>}<button type="button" disabled={busy} onClick={async()=>{await logoutAccount();location.assign('/login')}}>{t('Sign in with another method')}</button></div></main>
}
