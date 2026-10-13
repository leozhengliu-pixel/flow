import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Info, MoreHorizontal } from 'lucide-react'
import { toast } from '@/lib/toast'
import { useI18n } from '@/i18n/i18n'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { TeamCombobox } from '@/components/auth/oauth-team-combobox'
import { ScopeIcon } from '@/components/auth/oauth-scope-icon'
import { listApplications, saveApplication, uninstallApplication, type ApplicationInstallation } from '@/lib/application-agents'
import { fetchAuthorizedApplications, revokeOAuthAuthorization, type AuthorizedApplication } from '@/lib/api'
import { OAUTH_SCOPE_LABELS, consentPermissions } from '@/lib/oauth-consent'
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
      // Members see the applications they authorized themselves.
      const own = new Map<string, AppEntry>()
      for (const item of data.oauthAuthorizations ?? []) {
        if (item.revokedAt) continue
        const entry = own.get(item.clientId) ?? { clientId: item.clientId, name: item.clientName, authorizations: [] }
        entry.authorizations.push(item)
        own.set(item.clientId, entry)
      }
      setEntries([...own.values()])
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
            <OAuthAppAvatar logoUrl={entry.logoUrl || entry.installation?.avatarUrl}/>
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
  const entry = entries?.find(item => item.clientId === clientId)
  const ownApp = (data.oauthApplications ?? []).find(item => item.clientId === clientId)
  const name = entry?.name || ownApp?.name || ''
  const description = entry?.description || ownApp?.description
  const developerName = entry?.developerName || ownApp?.developerName
  const developerUrl = entry?.developerUrl || ownApp?.developerUrl
  const logoUrl = entry?.logoUrl || ownApp?.logoUrl || entry?.installation?.avatarUrl
  const authorizations = entry?.authorizations ?? []
  const installation = entry?.installation
  // The reference app lists the granted scopes alphabetically, without the narrower write scopes that `write` covers.
  const scopes = useMemo(() => consentPermissions([...new Set([...(entry?.authorizations ?? []).flatMap(item => item.scopes), ...(entry?.installation?.scopes ?? [])])].sort()).map(item => item.scope), [entry])
  const back = () => navigate(settingsPath(workspace, 'applications'))

  const revokeTokens = async () => {
    const confirmed = await confirmAction(t('Revoke access for "{name}"?').replace('{name}', name), { description: t('This will revoke all existing tokens for "{name}". You cannot undo this action.').replace('{name}', name), confirmLabel: t('Revoke'), danger: true })
    if (!confirmed) return
    try {
      for (const item of authorizations) await revokeOAuthAuthorization(item.id)
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
      <OAuthAppAvatar logoUrl={logoUrl} size={48}/>
      <div className="oauth-app-page-header-copy">
        <h1 data-i18n-ignore>{name}</h1>
        {developerName && <p data-i18n-ignore>{t('by {developer}').replace('{developer}', developerName)}</p>}
      </div>
      {admin && (ownApp || authorizations.length > 0) && <DropdownMenu>
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
        {scopes.map(scope => <div key={scope} className="oauth-app-permission"><ScopeIcon scope={scope}/><span>{t(OAUTH_SCOPE_LABELS[scope] ?? scope)}</span></div>)}
      </div> : <Row title={<span className="oauth-app-muted-title">{t('No permissions')}</span>}/>}
    </Section>
    {authorizations.length > 0 && <Section className="oauth-app-section" title={t('Authorizing members')} description={t('Members who have authorized this application to access workspace data')}>
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

type TeamMode = 'public' | 'mixed' | 'selected'

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
  const teams = data.teams.filter(team => !team.retiredAt)
  const choices: Team[] = mode === 'mixed' ? teams.filter(team => team.private) : teams
  const changed = mode !== initialMode || [...teamIds].sort().join() !== [...installedTeamIds].sort().join()
  const invalid = mode !== 'public' && !teamIds.length
  const update = async () => {
    setBusy(true)
    try {
      await saveApplication(data.workspace.urlKey, { ...installation, allPublicTeams: mode !== 'selected', teamIds: mode === 'public' ? [] : teamIds })
      await onSaved()
      toast.success(t('Team access updated'))
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
      {options.map(([value, label]) => <label key={value}>
        <input type="radio" name={`team-access-${installation.id}`} checked={mode === value} onChange={() => {
          setMode(value)
          setTeamIds(current => value === 'public' ? [] : value === 'mixed' ? current.filter(id => teams.find(team => team.id === id)?.private) : current)
        }}/>
        <span>{t(label)}</span>
      </label>)}
      {mode !== 'public' && <TeamCombobox teams={choices} value={teamIds} onChange={setTeamIds} t={t}/>}
      <footer><button type="button" className="oauth-app-button is-primary" disabled={!changed || invalid || busy} onClick={() => void update()}>{t('Update')}</button></footer>
    </div>
  </Section>
}
