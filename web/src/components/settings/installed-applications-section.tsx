import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Info, Lock, MoreHorizontal } from 'lucide-react'
import { toast } from '@/lib/toast'
import { useI18n } from '@/i18n/i18n'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { TeamCombobox } from '@/components/auth/oauth-team-combobox'
import { ScopeIcon } from '@/components/auth/oauth-scope-icon'
import { listApplications, saveApplication, uninstallApplication, type ApplicationInstallation } from '@/lib/application-agents'
import { fetchAuthorizedApplications, revokeMCPAccess, revokeOAuthAuthorization, type AuthorizedApplication } from '@/lib/api'
import { isMCPAuthorization, matchesApplication, mcpApplication } from '@/lib/mcp-application'
import { FlowLogo } from '@/components/ui/flow-logo'
import type { OAuthAuthorization } from '@/types/flow'
import { OAUTH_SCOPE_LABELS, REALTIME_PERMISSION, consentPermissions, nextTeamSelection, type TeamMode } from '@/lib/oauth-consent'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { TeamIcon } from '@/components/issue/issue-icons'
import { applicationSettingsPath, installedApplicationPath, settingsPath } from '@/lib/app-routes'
import type { BootstrapData, Team } from '@/types/flow'
import { SettingsCrumb, SettingsPageTitle as PageTitle, SettingsRow as Row, SettingsSection as Section } from './settings-primitives'
import { OAuthAppAvatar } from './oauth-applications-section'
import { fillNodes } from '@/lib/fill-nodes'
import './oauth-application-settings.css'
import './installed-applications-section.css'

type AppEntry = AuthorizedApplication & { installation?: ApplicationInstallation }

/** Loads the workspace's authorized applications (admins see every member's grants) and app-user installations. */
function useAuthorizedApplications(data: BootstrapData) {
  const admin = data.viewerRole === 'admin' || data.viewerRole === 'owner'
  const workspace = data.workspace.urlKey
  const [entries, setEntries] = useState<AppEntry[]>()
  const load = useCallback(async () => {
    if (!admin) {
      // Members see the applications they authorized themselves, every MCP client folded into "MCP".
      const own = new Map<string, AppEntry>()
      const mcp: OAuthAuthorization[] = []
      for (const item of data.oauthAuthorizations ?? []) {
        if (item.revokedAt) continue
        if (isMCPAuthorization(item)) {
          mcp.push(item)
          continue
        }
        const entry = own.get(item.clientId) ?? { clientId: item.clientId, name: item.clientName, authorizations: [] }
        entry.authorizations.push(item)
        own.set(item.clientId, entry)
      }
      const listed: AppEntry[] = [...own.values()]
      if (mcp.length) listed.push(mcpApplication(mcp, window.location.origin))
      setEntries(listed.sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())))
      return
    }
    const [authorized, installations] = await Promise.all([fetchAuthorizedApplications(), listApplications(workspace)])
    const installed = installations.filter(item => !item.builtin && item.active)
    setEntries(authorized.map(entry => ({ ...entry, installation: installed.find(item => item.clientId === entry.clientId) })))
  }, [admin, data.oauthAuthorizations, workspace])
  useEffect(() => { void load().catch(() => setEntries([])) }, [load])
  return { entries, reload: load, admin }
}

function appSubtitle(entry: AppEntry, data: BootstrapData, t: (value: string) => string, formatDate: (value: string, options?: Intl.DateTimeFormatOptions) => string) {
  if (entry.installation) {
    const installer = data.users.find(user => user.id === entry.installation?.installedBy)
    return t('App user installed by {member} on {date}').replace('{member}', installer?.displayName || installer?.name || t('a member')).replace('{date}', formatDate(entry.installation.createdAt, { month: 'short', day: 'numeric' }))
  }
  const count = new Set(entry.authorizations.map(item => item.userId)).size
  return count === 1 ? t('Authorized by 1 member') : t('Authorized by {count} members').replace('{count}', String(count))
}

/** The "MCP" entry's logo: Flow's own mark on its brand tile. */
function FlowAppLogo({ size = 32 }: { size?: number }) {
  return <span className="oauth-app-avatar app-logo is-flow" style={{ width: size, height: size }} role="img" aria-label="Flow">
    <FlowLogo size={size} variant="ridge"/>
  </span>
}

function ApplicationLogo({ entry, logoUrl, name, redirectUris, size }: { entry?: AppEntry; logoUrl?: string; name: string; redirectUris?: string[]; size?: number }) {
  if (entry?.kind === 'mcp') return <FlowAppLogo size={size}/>
  return <OAuthAppAvatar logoUrl={logoUrl} name={name} redirectUris={redirectUris} size={size}/>
}

/**
 * Settings › Applications: "Manage which third-party applications have access to your workspace." Every
 * application members authorized or installed as an app user; each opens its page.
 */
export function InstalledApplicationsPage({ data }: { data: BootstrapData }) {
  const { t, formatDate } = useI18n()
  const navigate = useNavigate()
  const { entries } = useAuthorizedApplications(data)
  return <div className="application-page">
    <PageTitle description={t('Manage which third-party applications have access to your workspace.')}>{t('Applications')}</PageTitle>
    {entries && !entries.length && <Section className="oauth-app-section">
      <p className="oauth-app-empty">{t('Your workspace has not yet authorized any external applications to connect with your Flow account')}</p>
    </Section>}
    {entries && entries.length > 0 && <Section className="oauth-app-section" title={t('Authorized applications')}>
      <ul className="oauth-app-list">
        {entries.map(entry => <li key={entry.clientId}>
          <a className="oauth-app-list-row" href={installedApplicationPath(data.workspace.urlKey, entry.clientId)} onClick={event => {
            if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
            event.preventDefault()
            navigate(installedApplicationPath(data.workspace.urlKey, entry.clientId))
          }}>
            <ApplicationLogo entry={entry} logoUrl={entry.logoUrl || entry.installation?.avatarUrl} name={entry.name} redirectUris={entry.redirectUris}/>
            <span className="oauth-app-list-copy">
              <strong data-i18n-ignore>{entry.name}</strong>
              <span data-i18n-ignore>{appSubtitle(entry, data, t, formatDate)}</span>
            </span>
          </a>
        </li>)}
      </ul>
    </Section>}
  </div>
}

/**
 * An application's page in Settings › Applications: its details, the permissions members granted, the
 * authorizing members and, for actor=app installs, the app user with its team access. "Revoke access" (header
 * menu) revokes every member's tokens; the app user's own menu removes the installation.
 */
export function InstalledApplicationPage({ data, clientId, onReload }: { data: BootstrapData; clientId: string; onReload: () => Promise<void> }) {
  const { t, formatDate } = useI18n()
  const navigate = useNavigate()
  const { entries, reload, admin } = useAuthorizedApplications(data)
  const workspace = data.workspace.urlKey
  // An MCP client's own (older) link opens the "MCP" entry it is folded into.
  const entry = entries?.find(item => matchesApplication(item, clientId))
  const mcp = entry?.kind === 'mcp'
  const ownApp = (data.oauthApplications ?? []).find(item => item.clientId === clientId)
  const name = entry?.name || ownApp?.name || ''
  const description = entry?.description || ownApp?.description
  const developerName = entry?.developerName || ownApp?.developerName
  const developerUrl = entry?.developerUrl || ownApp?.developerUrl
  const logoUrl = entry?.logoUrl || ownApp?.logoUrl || entry?.installation?.avatarUrl
  const authorizations = entry?.authorizations ?? []
  const installation = entry?.installation
  // The reference app lists the granted permissions alphabetically (by their wording), without the narrower write scopes that `write` covers, and
  // "Receive realtime updates about your workspace" while the application has a webhook and is authorized.
  const scopes = useMemo(() => {
    const granted = [...(entry?.permissions?.map(item => item.scope) ?? (entry?.authorizations ?? []).flatMap(item => item.scopes)), ...(entry?.installation?.scopes ?? [])]
    if (entry?.realtime && granted.length) granted.push(REALTIME_PERMISSION)
    return consentPermissions([...new Set(granted)]).sort((a, b) => a.label.localeCompare(b.label, 'en')).map(item => item.scope)
  }, [entry])
  // The "MCP" entry marks each permission an authorization used recently "Actively used".
  const activeScopes = useMemo(() => entry?.permissions ? new Set(entry.permissions.filter(item => item.activelyUsed).map(item => item.scope)) : undefined, [entry])
  const back = () => navigate(settingsPath(workspace, 'applications'))

  const revokeTokens = async () => {
    const confirmed = await confirmAction(t('Revoke access for "{name}"?').replace('{name}', name), { description: t('This will revoke all existing tokens for "{name}". You cannot undo this action.').replace('{name}', name), confirmLabel: t('Revoke'), danger: true })
    if (!confirmed) return
    try {
      // MCP: every MCP client's authorizations at once (an admin's revoke covers every member, a member's their own).
      if (mcp) await revokeMCPAccess()
      else for (const item of authorizations) await revokeOAuthAuthorization(item.id)
      await Promise.all([reload(), onReload()])
      toast.success(t('Tokens revoked'), { description: t('The tokens for "{name}" have been revoked.').replace('{name}', name) })
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not revoke access'))
    }
  }
  const revokeInstallation = async () => {
    if (!installation) return
    const confirmed = await confirmAction(t('Revoke access for {name}?').replace('{name}', name), { description: t('The application will lose access to the workspace for now. It can be reauthorized in the future.'), confirmLabel: t('Confirm'), danger: true })
    if (!confirmed) return
    try {
      await uninstallApplication(workspace, installation.id)
      await Promise.all([reload(), onReload()])
      toast.success(t('Access revoked'), { description: t('Access has been revoked for {name}').replace('{name}', name) })
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not revoke access'))
    }
  }

  if (entries && !entry && !ownApp) return <div className="application-page">
    <SettingsCrumb pinned onClick={back}>{t('Applications')}</SettingsCrumb>
    <PageTitle>{t('Application not found')}</PageTitle>
  </div>

  return <div className="application-page">
    <SettingsCrumb pinned onClick={back}>{t('Applications')}</SettingsCrumb>
    <header className="oauth-app-page-header">
      <ApplicationLogo entry={entry} logoUrl={logoUrl} name={name} redirectUris={entry?.redirectUris ?? ownApp?.redirectUris} size={48}/>
      <div className="oauth-app-page-header-copy">
        <h1 data-i18n-ignore>{name}</h1>
        {developerName && <p data-i18n-ignore>{t('by {developer}').replace('{developer}', developerName)}</p>}
      </div>
      {(admin || mcp) && (ownApp || authorizations.length > 0) && <DropdownMenu>
        <DropdownMenuTrigger className="oauth-app-icon-button is-small" aria-label={t('Open menu')}><MoreHorizontal aria-hidden="true"/></DropdownMenuTrigger>
        <DropdownMenuContent align="end" sideOffset={4} className="oauth-app-menu">
          {ownApp && <DropdownMenuItem onSelect={() => navigate(applicationSettingsPath(workspace, ownApp.id))}>{t('View application settings')}</DropdownMenuItem>}
          {authorizations.length > 0 && <DropdownMenuItem onSelect={() => void revokeTokens()}>{t('Revoke access')}</DropdownMenuItem>}
        </DropdownMenuContent>
      </DropdownMenu>}
    </header>
    {(description || developerUrl) && <Section className="oauth-app-section">
      {description && <Row title={t('Description')} description={<span data-i18n-ignore>{description}</span>}/>}
      {developerUrl && <Row title={t('Developer website')} description={<span data-i18n-ignore>{developerUrl}</span>}/>}
    </Section>}
    <Section className="oauth-app-section" title={t('Permissions')}>
      {scopes.length ? <div className="oauth-app-permissions">
        {scopes.map(scope => <div key={scope} className="oauth-app-permission"><ScopeIcon scope={scope}/>
          {activeScopes ? <span className="oauth-app-permission-copy">
            <span>{t(OAUTH_SCOPE_LABELS[scope] ?? scope)}</span>
            {activeScopes.has(scope) && <span className="oauth-app-permission-note">{t('Actively used')}</span>}
          </span> : <span>{t(OAUTH_SCOPE_LABELS[scope] ?? scope)}</span>}
        </div>)}
      </div> : <Row title={<span className="oauth-app-muted-title">{t('No permissions')}</span>}/>}
    </Section>
    {mcp && entry.members && entry.members.length > 0 && <Section className="oauth-app-section" title={t('Authorizing members')} description={t('Members who have authorized this application to access workspace data')}>
      {entry.members.map(item => {
        const user = data.users.find(member => member.id === item.userId)
        const authorized = t('Authorized on {date}').replace('{date}', formatDate(item.authorizedAt, { month: 'short', day: 'numeric' }))
        return <div key={item.userId} className="oauth-app-member">
          <UserAvatar className="oauth-app-member-avatar" name={user?.displayName || user?.name || '?'} avatarUrl={user?.avatarUrl}/>
          <div className="oauth-app-member-copy">
            <span data-i18n-ignore>{user?.displayName || user?.name || t('Unknown user')}{user?.username && <em>({user.username})</em>}</span>
            <span data-i18n-ignore>{item.lastUsedAt ? `${authorized} · ${t('Last used on {date}').replace('{date}', formatDate(item.lastUsedAt, { month: 'short', day: 'numeric' }))}` : authorized}</span>
          </div>
        </div>
      })}
    </Section>}
    {!mcp && authorizations.length > 0 && <Section className="oauth-app-section" title={t('Authorizing members')} description={t('Members who have authorized this application to access workspace data')}>
      {authorizations.map(item => {
        const user = data.users.find(member => member.id === item.userId)
        return <div key={item.id} className="oauth-app-member">
          <UserAvatar className="oauth-app-member-avatar" name={user?.displayName || user?.name || '?'} avatarUrl={user?.avatarUrl}/>
          <div className="oauth-app-member-copy">
            <span data-i18n-ignore>{user?.displayName || user?.name || t('Unknown user')}{user?.username && <em>({user.username})</em>}</span>
            <span>{t('Authorized on {date}').replace('{date}', formatDate(item.createdAt, { month: 'short', day: 'numeric' }))}</span>
          </div>
        </div>
      })}
    </Section>}
    {installation && <AppUserSection data={data} name={name} installation={installation} onRevoke={() => void revokeInstallation()} onSaved={async () => { await Promise.all([reload(), onReload()]) }}/>}
    {entries && !authorizations.length && !installation && <Section className="oauth-app-section" title={t('Installation status')}>
      <Row title={<span className="oauth-app-muted-title">{t('No installations')}</span>}/>
    </Section>}
  </div>
}

/** The reference app's "App user" card: the app user, who installed it and its team access (with Update). */
function AppUserSection({ data, name, installation, onRevoke, onSaved }: { data: BootstrapData; name: string; installation: ApplicationInstallation; onRevoke: () => void; onSaved: () => Promise<void> }) {
  const { t, formatDate } = useI18n()
  const appUser = data.users.find(user => user.id === installation.userId)
  const installer = data.users.find(user => user.id === installation.installedBy)
  // The server sends null for "no teams selected".
  const installedTeamIds = installation.teamIds ?? []
  const installedScopes = installation.scopes ?? []
  const initialMode: TeamMode = installation.allPublicTeams ? (installedTeamIds.length ? 'mixed' : 'public') : 'selected'
  const [mode, setMode] = useState<TeamMode>(initialMode)
  const [teamIds, setTeamIds] = useState<string[]>(installedTeamIds)
  const [busy, setBusy] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const teams = data.teams.filter(team => !team.retiredAt && !team.archivedAt)
  const changed = mode !== initialMode || [...teamIds].sort().join() !== [...installedTeamIds].sort().join()
  const invalid = mode !== 'public' && !teamIds.length
  const update = async () => {
    setConfirming(false)
    setBusy(true)
    try {
      await saveApplication(data.workspace.urlKey, { ...installation, allPublicTeams: mode !== 'selected', teamIds: mode === 'public' ? [] : teamIds })
      await onSaved()
      toast.success(t('Team access updated'), { description: t('The application’s team access has been updated.') })
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t('Could not update team access'))
    } finally {
      setBusy(false)
    }
  }
  const options: [TeamMode, string][] = [['public', 'All public teams'], ['mixed', 'All public teams and select other teams…'], ['selected', 'Only select teams…']]
  return <Section className="oauth-app-section" title={t('App user')} description={t('This application can take actions as a workspace user within teams it can access')}>
    <div className="oauth-app-member">
      <UserAvatar className="oauth-app-member-avatar" name={appUser?.displayName || name} avatarUrl={appUser?.avatarUrl || installation.avatarUrl}/>
      <div className="oauth-app-member-copy">
        <span data-i18n-ignore>{appUser?.displayName || name}{appUser?.username && <em>({appUser.username})</em>}<span className="oauth-app-badge">{t('Agent')}</span></span>
        <span>
          <span data-i18n-ignore>{fillNodes(t('Installed by {member} on {date}'), { member: installer?.displayName || installer?.name || t('a member'), date: formatDate(installation.createdAt, { month: 'short', day: 'numeric' }) })}</span>
          <FlowTooltip label={installedScopes.map(scope => t(OAUTH_SCOPE_LABELS[scope] ?? scope)).join('\n')}>
            <span className="oauth-app-scope-count"><Info aria-hidden="true"/>{installedScopes.length === 1 ? t('1 permission') : t('{count} permissions').replace('{count}', String(installedScopes.length))}</span>
          </FlowTooltip>
        </span>
      </div>
      <DropdownMenu>
        <DropdownMenuTrigger className="oauth-app-icon-button" aria-label={t('App user options')}><MoreHorizontal aria-hidden="true"/></DropdownMenuTrigger>
        <DropdownMenuContent align="end" sideOffset={4} className="oauth-app-menu">
          <DropdownMenuItem onSelect={onRevoke}>{t('Revoke access…')}</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
    <div className="oauth-app-team-access" role="radiogroup" aria-label={t('Team access')}>
      <strong>{t('Team access')}</strong>
      {options.map(([value, label]) => <Fragment key={value}>
        <label>
          <input type="radio" name={`team-access-${installation.id}`} checked={mode === value} onChange={() => {
            setMode(value)
            setTeamIds(current => nextTeamSelection(value, current, teams))
          }}/>
          <span>{t(label)}</span>
        </label>
        {mode === value && value !== 'public' && <TeamCombobox teams={value === 'mixed' ? teams.filter(team => team.private) : teams} value={teamIds} onChange={setTeamIds} placeholder={value === 'mixed' ? 'Select other teams…' : 'Select teams…'} t={t}/>}
      </Fragment>)}
      <footer><button type="button" className="oauth-app-button is-primary" disabled={!changed || invalid || busy} onClick={() => setConfirming(true)}>{t('Update')}</button></footer>
    </div>
    <TeamAccessConfirmDialog open={confirming} name={name} from={{ mode: initialMode, teams: installedTeamIds }} to={{ mode, teams: teamIds }} teams={data.teams} onCancel={() => setConfirming(false)} onConfirm={() => void update()}/>
  </Section>
}

type TeamAccess = { mode: TeamMode; teams: string[] }

/**
 * The reference app's "Update team access for <App>?" confirmation: what the application can access now and what it will
 * access, with the teams listed under "…these other teams:" / "these teams:".
 */
function TeamAccessConfirmDialog({ open, name, from, to, teams, onCancel, onConfirm }: { open: boolean; name: string; from: TeamAccess; to: TeamAccess; teams: Team[]; onCancel: () => void; onConfirm: () => void }) {
  const { t } = useI18n()
  const describe = (template: string, access: TeamAccess) => {
    const scope = access.mode === 'public' ? t('all public teams.') : access.mode === 'mixed' ? t('all public teams and these other teams:') : t('these teams:')
    const listed = access.mode === 'public' ? [] : access.teams.map(id => teams.find(team => team.id === id)).filter((team): team is Team => Boolean(team))
    return <>
      <p>{fillNodes(t(template), { access: <strong key="access">{scope}</strong> })}</p>
      {listed.length > 0 && <ul className="oauth-app-confirm-teams">
        {listed.map(team => <li key={team.id}><TeamIcon team={team} size={14}/><span data-i18n-ignore>{team.name}</span>{team.private && <Lock aria-hidden="true"/>}</li>)}
      </ul>}
    </>
  }
  return <Dialog open={open} onOpenChange={next => { if (!next) onCancel() }}>
    <DialogContent className="action-dialog oauth-app-confirm" overlayClassName="action-dialog-overlay">
      <DialogTitle data-i18n-ignore>{t('Update team access for {name}?').replace('{name}', name)}</DialogTitle>
      <div className="oauth-app-confirm-body" data-i18n-ignore>
        {describe('The application currently has access to {access}', from)}
        {describe('The application will now have access to {access}', to)}
      </div>
      <footer>
        <button type="button" onClick={onCancel}>{t('Cancel')}</button>
        <button type="button" className="primary" onClick={onConfirm}>{t('Confirm')}</button>
      </footer>
    </DialogContent>
  </Dialog>
}
