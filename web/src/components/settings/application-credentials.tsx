import { useState } from 'react'
import { Copy, RotateCw } from 'lucide-react'
import { toast } from '@/lib/toast'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { FlowTooltip } from '@/components/ui/tooltip'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { createApplicationDeveloperToken, rotateOAuthApplicationSecret, updateOAuthApplication, type ApplicationToken } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { OAuthApplication } from '@/types/flow'
import { SettingsRow, SettingsSection, SettingsToggle } from './settings-primitives'
import './application-credentials.css'

const personalScopes = ['read', 'write', 'create_issues', 'create_comments']

export function ApplicationCredentials({ app, onReload }: { app: OAuthApplication; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [actor, setActor] = useState<'app' | 'user'>('app')
  const [scopes, setScopes] = useState<string[]>(['read'])
  const [token, setToken] = useState<ApplicationToken>()
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const choices = app.scopes.filter(scope => actor === 'app' || personalScopes.includes(scope))
  const copy = async (value: string) => {
    try { await navigator.clipboard.writeText(value); toast.success(t('Copied to clipboard')) }
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
    if (!await confirmAction(t('Rotate client secret?'), { description: t('Existing client credentials tokens will stop working.'), confirmLabel: t('Rotate'), danger: true })) return
    setBusy(true)
    try { setSecret((await rotateOAuthApplicationSecret(app.id)).clientSecret); await onReload() }
    catch (error) { toast.error(error instanceof Error ? error.message : t('Could not rotate secret')) }
    finally { setBusy(false) }
  }
  return <>
    <SettingsSection title={t('OAuth credentials')}>
      <SettingsRow title={t('Availability')} description={t('Allow installation in other workspaces')}>
        <SettingsToggle label={t('Allow installation in other workspaces')} checked={Boolean(app.publicEnabled)} disabled={busy} onChange={() => {
          setBusy(true)
          void updateOAuthApplication(app.id, { publicEnabled: !app.publicEnabled }).then(onReload).catch(error => toast.error(String(error.message ?? error))).finally(() => setBusy(false))
        }}/>
      </SettingsRow>
      <SettingsRow title={t('Client credentials')} description={t('Allow server-to-server access as this application.')}>
        <SettingsToggle label={t('Client credentials')} checked={Boolean(app.supportsClientCredentials)} disabled={busy} onChange={() => {
          setBusy(true)
          void updateOAuthApplication(app.id, { supportsClientCredentials: !app.supportsClientCredentials }).then(onReload).catch(error => toast.error(String(error.message ?? error))).finally(() => setBusy(false))
        }}/>
      </SettingsRow>
      <SettingsRow title={t('Client secret')}>
        <div className="application-credential-secret"><code data-i18n-ignore>{secret || '****************'}</code>
          <FlowTooltip label={t('Rotate secret')}><button className="settings-icon-action" aria-label={t('Rotate secret')} disabled={busy} onClick={() => void rotate()}><RotateCw size={14}/></button></FlowTooltip>
          {secret && <FlowTooltip label={t('Copy to clipboard')}><button className="settings-icon-action" aria-label={t('Copy client secret')} onClick={() => void copy(secret)}><Copy size={14}/></button></FlowTooltip>}
        </div>
      </SettingsRow>
      <SettingsRow title={t('Developer token')}>
        <button type="button" className="settings-action" onClick={() => { setActor('app'); setScopes(['read']); setToken(undefined); setError(''); setOpen(true) }}>{t('Create & copy token')}</button>
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
