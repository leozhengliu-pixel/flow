import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Command } from 'cmdk'
import { ArrowUpRight, Copy, RefreshCw } from 'lucide-react'
import { toast } from '@/lib/toast'
import { Description as DialogDescription } from '@radix-ui/react-dialog'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { FlowTooltip } from '@/components/ui/tooltip'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { ScopeIcon } from '@/components/auth/oauth-scope-icon'
import { createApplicationDeveloperToken, fetchApplicationDeveloperToken, fetchOAuthApplicationClientSecret, rotateOAuthApplicationSecret, type ApplicationToken } from '@/lib/api'
import { installedApplicationPath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { OAuthApplication } from '@/types/flow'
import { SettingsRow, SettingsSection, SettingsToggle } from './settings-primitives'
import './application-credentials.css'
import './oauth-application-settings.css'

/** The reference app masks the client secret with 32 bullets; the copy button copies the real secret. */
const MASKED_SECRET = '•'.repeat(32)
const ACTOR_DOCS_URL = 'https://flow.app/docs/oauth#actorapp'
const REVOKE_DOCS_URL = 'https://flow.app/docs/oauth#revoking-tokens-rfc-7009'

/** The reference app's "Select scopes" choices for an app user token, with its descriptions (read and write start on). */
const APP_TOKEN_SCOPES: { scope: string; label: string; description: string }[] = [
  { scope: 'read', label: 'read', description: 'Read team-accessible data' },
  { scope: 'write', label: 'write', description: 'Read and write team-accessible data' },
  { scope: 'app:mentionable', label: 'app:mentionable', description: 'Mention the app' },
  { scope: 'app:assignable', label: 'app:assignable', description: 'Delegate issues to the app or add the app as a project member' },
  { scope: 'customer:read', label: 'customer:read', description: 'Read data about customers in the workspace' },
  { scope: 'customer:write', label: 'customer:write', description: 'Read and write data about customers in the workspace' },
  { scope: 'initiative:read', label: 'initiative:read', description: 'Read data about initiatives in the workspace' },
  { scope: 'initiative:write', label: 'initiative:write', description: 'Read and write data about initiatives in the workspace' },
]

/**
 * The reference app's "OAuth credentials" section: Client ID (copy), Client secret (masked; rotate and copy) and the developer
 * token. "Create & copy token" opens the reference app's command menu ("Create developer access token…": User / App). User copies a
 * new read/write token for you; App copies the app user's existing token, or asks for scopes and creates one.
 */
export function ApplicationCredentials({ app, workspace, onReload }: { app: OAuthApplication; workspace: string; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const navigate = useNavigate()
  const [picking, setPicking] = useState(false)
  const [scoping, setScoping] = useState(false)
  const [busy, setBusy] = useState(false)
  const copy = async (value: string | (() => Promise<string>)) => {
    try { await navigator.clipboard.writeText(typeof value === 'string' ? value : await value()); toast.success(t('Copied to clipboard')) }
    catch { toast.error(t('Could not copy to clipboard')) }
  }
  const copied = async (token: ApplicationToken) => {
    await navigator.clipboard.writeText(token.access_token)
    if (!token.existing) {
      toast.success(t('Developer access token created and copied to your clipboard'))
      return
    }
    const scopes = token.scope.split(' ').filter(Boolean)
    const detailsPath = installedApplicationPath(workspace, app.clientId)
    const [before, after = ''] = t('The existing token has these scopes: {scopes}. If you’d like to create a new token with different scopes, first revoke the existing token from the {link}.').split('{scopes}')
    const [middle, end = ''] = after.split('{link}')
    toast.success(t('Existing app token copied to clipboard'), {
      description: <>
        <span>{before}{scopes.map((scope, index) => <span key={scope}>{index > 0 && ', '}<code className="oauth-app-code" data-i18n-ignore>{scope}</code></span>)}{middle}<a href={detailsPath} onClick={event => { event.preventDefault(); navigate(detailsPath) }}>{t('app details page')}</a>{end}</span>
        <br/>
        <span>{t('Or revoke it via the {link}.').split('{link}').map((part, index) => index === 0 ? <span key="before">{part}</span> : <span key="after"><a href={REVOKE_DOCS_URL} target="_blank" rel="noreferrer">{t('OAuth API')}<span aria-hidden="true" className="oauth-app-link-arrow">↗</span></a>{part}</span>)}</span>
      </>,
    })
  }
  const run = async (task: () => Promise<void>) => {
    if (busy) return
    setBusy(true)
    try { await task() }
    catch (error) { toast.error(error instanceof Error && error.message ? error.message : t('Could not create token')) }
    finally { setBusy(false) }
  }
  const choose = (actor: 'user' | 'app') => {
    setPicking(false)
    void run(async () => {
      if (actor === 'user') {
        await copied(await createApplicationDeveloperToken(app.id, 'user'))
        return
      }
      const existing = await fetchApplicationDeveloperToken(app.id)
      if (existing) await copied(existing)
      else setScoping(true)
    })
  }
  const createAppToken = async (scopes: string[]) => {
    await run(async () => {
      await copied(await createApplicationDeveloperToken(app.id, 'app', scopes))
      setScoping(false)
      await onReload()
    })
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
        <button type="button" className="oauth-app-button is-ghost" disabled={busy} onClick={() => setPicking(true)}>{t('Create & copy token')}</button>
      </SettingsRow>
    </SettingsSection>
    <DeveloperTokenActorMenu open={picking} onOpenChange={setPicking} onChoose={choose}/>
    <AppTokenScopesDialog key={scoping ? 'open' : 'closed'} open={scoping} available={app.scopes} busy={busy} onOpenChange={next => { if (!busy) setScoping(next) }} onCreate={scopes => void createAppToken(scopes)}/>
  </>
}

/** The reference app's command menu for "Create & copy token": search "Create developer access token…", then User or App. */
function DeveloperTokenActorMenu({ open, onOpenChange, onChoose }: { open: boolean; onOpenChange: (open: boolean) => void; onChoose: (actor: 'user' | 'app') => void }) {
  const { t } = useI18n()
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="command-dialog" overlayClassName="command-overlay" aria-describedby={undefined} onOpenAutoFocus={event => event.preventDefault()}>
      <DialogTitle className="sr-only">{t('Create developer access token…')}</DialogTitle>
      <Command loop>
        <div className="command-input">
          <Command.Input aria-label={t('Command menu')} placeholder={t('Create developer access token…')} autoFocus/>
        </div>
        <Command.List>
          {(['user', 'app'] as const).map(actor => <Command.Item key={actor} value={actor === 'user' ? t('User') : t('App')} onSelect={() => onChoose(actor)}>
            <span>{t(actor === 'user' ? 'User' : 'App')}</span>
          </Command.Item>)}
          <Command.Empty>{t('No results found.')}</Command.Empty>
        </Command.List>
      </Command>
    </DialogContent>
  </Dialog>
}

/** The reference app's "Select scopes" dialog for a new app user token: one switch per scope, read and write on by default. */
function AppTokenScopesDialog({ open, available, busy, onOpenChange, onCreate }: { open: boolean; available: string[]; busy: boolean; onOpenChange: (open: boolean) => void; onCreate: (scopes: string[]) => void }) {
  const { t } = useI18n()
  const choices = APP_TOKEN_SCOPES.filter(item => available.includes(item.scope))
  const [selected, setSelected] = useState(() => choices.filter(item => item.scope === 'read' || item.scope === 'write').map(item => item.scope))
  const toggle = (scope: string, on: boolean) => setSelected(current => on ? [...current, scope] : current.filter(item => item !== scope))
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="application-scopes-dialog" closeLabel={t('Close')}>
      <DialogTitle>{t('Select scopes')}</DialogTitle>
      <DialogDescription>{t('Choose which OAuth scopes to grant for this app user token.')} <a className="feature-docs-link" href={ACTOR_DOCS_URL} target="_blank" rel="noreferrer">{t('Docs')}<ArrowUpRight size={11} aria-hidden="true"/></a></DialogDescription>
      <ul className="application-scopes-list">
        {choices.map(item => <li key={item.scope}>
          <span className="application-scopes-icon"><ScopeIcon scope={item.scope}/></span>
          <span className="application-scopes-copy"><strong data-i18n-ignore>{item.label}</strong><span>{t(item.description)}</span></span>
          <SettingsToggle label={item.label} checked={selected.includes(item.scope)} disabled={busy} onChange={on => toggle(item.scope, on)}/>
        </li>)}
      </ul>
      <footer>
        <button type="button" className="oauth-app-button" disabled={busy} onClick={() => onOpenChange(false)}>{t('Cancel')}</button>
        <button type="button" className="oauth-app-button is-primary" disabled={busy || !selected.length} onClick={() => onCreate(APP_TOKEN_SCOPES.map(item => item.scope).filter(scope => selected.includes(scope)))}>{t('Create token')}</button>
      </footer>
    </DialogContent>
  </Dialog>
}
