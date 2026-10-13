import { useState } from 'react'
import { Copy, RefreshCw } from 'lucide-react'
import { toast } from '@/lib/toast'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { FlowTooltip } from '@/components/ui/tooltip'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { createApplicationDeveloperToken, fetchOAuthApplicationClientSecret, rotateOAuthApplicationSecret, type ApplicationToken } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { OAuthApplication } from '@/types/flow'
import { SettingsRow, SettingsSection } from './settings-primitives'
import './application-credentials.css'
import './oauth-application-settings.css'

const personalScopes = ['read', 'write', 'create_issues', 'create_comments']
/** The reference app masks the client secret with 32 bullets; the copy button copies the real secret. */
const MASKED_SECRET = '•'.repeat(32)

/**
 * The reference app's "OAuth credentials" section: Client ID (copy), Client secret (masked; rotate and copy) and the developer
 * token ("Create & copy token").
 */
export function ApplicationCredentials({ app, onReload }: { app: OAuthApplication; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [actor, setActor] = useState<'app' | 'user'>('app')
  const [scopes, setScopes] = useState<string[]>(['read'])
  const [token, setToken] = useState<ApplicationToken>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const choices = app.scopes.filter(scope => actor === 'app' || personalScopes.includes(scope))
  const copy = async (value: string | (() => Promise<string>)) => {
    try { await navigator.clipboard.writeText(typeof value === 'string' ? value : await value()); toast.success(t('Copied to clipboard')) }
    catch { toast.error(t('Could not copy to clipboard')) }
  }
  const create = async () => {
    if (busy) return
    setBusy(true); setError('')
    try {
      const result = await createApplicationDeveloperToken(app.id, actor, scopes)
      setToken(result)
      await copy(result.access_token)
      await onReload()
    } catch (error) { setError(error instanceof Error ? error.message : t('Could not create token')) }
    finally { setBusy(false) }
  }
  const rotate = async () => {
    if (!await confirmAction(t('Rotate your client secret?'), { description: t('You cannot undo this action.'), confirmLabel: t('Rotate') })) return
    setBusy(true)
    try { await rotateOAuthApplicationSecret(app.id); await onReload(); toast.success(t('Client secret rotated')) }
    catch (error) { toast.error(error instanceof Error ? error.message : t('Could not rotate secret')) }
    finally { setBusy(false) }
  }
  return <>
    <SettingsSection className="oauth-app-section" title={t('OAuth credentials')} description={t('For authenticating your application with Flow’s API. Never share your secret publicly.')}>
      <SettingsRow title={t('Client ID')} description={<span className="oauth-app-mono" data-i18n-ignore>{app.clientId}</span>}>
        <FlowTooltip label={t('Copy to clipboard')}><button type="button" className="oauth-app-icon-button" aria-label={t('Copy client ID')} onClick={() => void copy(app.clientId)}><Copy aria-hidden="true"/></button></FlowTooltip>
      </SettingsRow>
      <SettingsRow title={t('Client secret')} description={<span className="oauth-app-mono oauth-app-secret" aria-label={t('Client secret (hidden)')} data-i18n-ignore>{MASKED_SECRET}</span>}>
        <div className="oauth-app-actions">
          <FlowTooltip label={t('Rotate secret')}><button type="button" className="oauth-app-icon-button" aria-label={t('Rotate secret')} disabled={busy} onClick={() => void rotate()}><RefreshCw aria-hidden="true"/></button></FlowTooltip>
          <FlowTooltip label={t('Copy to clipboard')}><button type="button" className="oauth-app-icon-button" aria-label={t('Copy client secret')} onClick={() => void copy(async () => (await fetchOAuthApplicationClientSecret(app.id)).clientSecret)}><Copy aria-hidden="true"/></button></FlowTooltip>
        </div>
      </SettingsRow>
      <SettingsRow title={t('Developer token')}>
        <button type="button" className="oauth-app-button is-ghost" onClick={() => { setActor('app'); setScopes(['read']); setToken(undefined); setError(''); setOpen(true) }}>{t('Create & copy token')}</button>
      </SettingsRow>
    </SettingsSection>
    <Dialog open={open} onOpenChange={next => { if (busy) return; setOpen(next); if (!next) setToken(undefined) }}>
      <DialogContent className="application-token-dialog" aria-describedby={undefined} closeLabel={t('Close')}>
        <DialogTitle>{t(token ? 'Developer token' : 'Create developer token')}</DialogTitle>
        {token ? <div className="application-token-result">
          <input aria-label={t('Access token')} readOnly value={token.access_token} data-i18n-ignore/>
          <button className="settings-icon-action" aria-label={t('Copy access token')} onClick={() => void copy(token.access_token)}><Copy size={16}/></button>
        </div> : <>
          <fieldset><legend>{t('Token actor')}</legend>{(['app', 'user'] as const).map(value => <label key={value}><input type="radio" name="developer-token-actor" checked={actor === value} onChange={() => { setActor(value); setScopes(['read']) }}/>{t(value === 'app' ? 'Application' : 'User')}</label>)}</fieldset>
          <fieldset><legend>{t('Permissions')}</legend>{choices.map(scope => <label key={scope}><input type="checkbox" checked={scopes.includes(scope)} disabled={scope === 'read'} onChange={event => setScopes(current => event.target.checked ? [...current, scope] : current.filter(item => item !== scope))}/><code data-i18n-ignore>{scope}</code></label>)}</fieldset>
        </>}
        {error && <p role="alert">{error}</p>}
        <footer>{token ? <button className="settings-action" onClick={() => { setOpen(false); setToken(undefined) }}>{t('Done')}</button> : <button className="settings-action primary" disabled={busy} onClick={() => void create()}>{t(busy ? 'Creating…' : 'Create token')}</button>}</footer>
      </DialogContent>
    </Dialog>
  </>
}
