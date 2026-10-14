import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowRightLeft, Check, ChevronDown } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'

import { decideOAuthAuthorization, fetchOAuthAuthorizationRequest, fetchUserSettings, type OAuthAuthorizationRequest } from '@/lib/api'
import { applyAccountTheme } from '@/lib/theme'
import { request as apiRequest } from '@/lib/api-client'
import { settingsPath, workspaceOnboardingPath } from '@/lib/app-routes'
import { useOptionalTranslate } from '@/i18n/i18n'
import { consentPermissions, nextTeamSelection, oauthErrorMessage, OAUTH_SCOPE_LABELS, reconsentPermissions, REALTIME_PERMISSION, type InstalledConsent, type TeamMode } from '@/lib/oauth-consent'
import type { AccountBootstrap, Team, WorkspaceMembership } from '@/types/flow'
import { FlowLogo } from '@/components/ui/flow-logo'
import { GridLoader } from '@/components/ui/grid-loader'
import { WorkspaceAvatar } from '@/components/workspace/workspace-menu'
import { ApplicationIconPlaceholder } from '@/components/ui/application-icon-placeholder'
import { TeamCombobox } from './oauth-team-combobox'
import { ScopeIcon } from './oauth-scope-icon'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'

import './oauth-authorize-page.css'

type Props = { account: AccountBootstrap }
type Translate = (source: string) => string

/**
 * /oauth/authorize — the reference app's consent screen: a workspace switcher in the corner, one card with both marks,
 * "<App> is requesting access", the requested permissions (and, for actor=app installs, team access), Cancel /
 * Authorize, and the data-sharing notice under the card. Invalid requests show the mark and the error, centered.
 * An actor=app request for an application already installed in the workspace shows the reference app's re-consent card
 * instead: "Existing permissions" and "Requested permissions" with "Update access", or "<App> already installed"
 * with Continue when nothing new is asked; neither asks for team access nor shows the notice.
 */
export function OAuthAuthorizePage({ account }: Props) {
  const location = useLocation()
  return <OAuthConsentPage key={JSON.stringify([account.viewer.id, location.search])} account={account}/>
}

function OAuthConsentPage({ account }: Props) {
  const t = useOptionalTranslate()
  const location = useLocation()
  const navigate = useNavigate()
  const query = useMemo(() => new URLSearchParams(location.search), [location.search])
  const [request, setRequest] = useState<OAuthAuthorizationRequest>()
  const [workspaceKey, setWorkspaceKey] = useState('')
  const [pending, setPending] = useState(false)
  const [loadError, setLoadError] = useState('')
  const [error, setError] = useState('')
  const [autoApproving, setAutoApproving] = useState(false)
  const [teams, setTeams] = useState<Team[]>([])
  const [teamIds, setTeamIds] = useState<string[]>([])
  const [teamMode, setTeamMode] = useState<TeamMode>('public')
  // actor=app: the workspace's installation of this application (null when not installed, undefined while loading).
  const [installed, setInstalled] = useState<InstalledConsent | null>()
  const isApplication = query.get('actor') === 'app'
  const pendingRef = useRef(false)
  const submitRef = useRef<(approve: boolean, workspaceOverride?: string) => Promise<void>>(async () => {})
  const activeRef = useRef(true)
  const preferredWorkspaceRef = useRef(account.lastWorkspaceKey)
  preferredWorkspaceRef.current = account.lastWorkspaceKey
  const themeWorkspace = account.workspaces.find(item => item.workspace.urlKey === account.lastWorkspaceKey)?.workspace.urlKey ?? account.workspaces[0]?.workspace.urlKey

  useEffect(() => {
    let active = true
    if (themeWorkspace) void fetchUserSettings(themeWorkspace).then(settings => { if (active) applyAccountTheme(settings) }).catch(() => undefined)
    return () => { active = false }
  }, [themeWorkspace])

  useEffect(() => {
    const controller = new AbortController()
    activeRef.current = true
    fetchOAuthAuthorizationRequest(location.search, controller.signal)
      .then(value => {
        if (controller.signal.aborted) return
        setRequest(value)
        // The user already approved this client for a workspace: skip the consent screen and continue straight away.
        const remembered = value.previouslyAuthorizedWorkspaceKey
        if (remembered && value.prompt !== 'consent' && new URLSearchParams(location.search).get('actor') !== 'app' && value.workspaces.some(item => item.workspace.urlKey === remembered)) {
          setWorkspaceKey(remembered)
          setAutoApproving(true)
          void submitRef.current(true, remembered)
          return
        }
        const preferred = value.workspaces.find(item => item.workspace.urlKey === preferredWorkspaceRef.current) ?? value.workspaces[0]
        if (preferred) setWorkspaceKey(preferred.workspace.urlKey)
      })
      .catch(value => { if (!controller.signal.aborted) setLoadError(oauthErrorMessage(value, t)) })
    return () => { controller.abort(); activeRef.current = false }
  // `t` only words the error; reloading the request when the locale loads would repeat the request.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.search])

  const clientId = query.get('client_id') ?? ''
  useEffect(() => {
    setTeams([]); setTeamIds([]); setTeamMode('public'); setInstalled(undefined)
    if (!isApplication || !workspaceKey) return
    const abort = new AbortController()
    const workspace = encodeURIComponent(workspaceKey)
    void apiRequest<Team[]>(`/api/oauth/installation-teams?workspace=${workspace}`, { signal: abort.signal })
      .then(value => { if (!abort.signal.aborted) setTeams(value) })
      .catch(value => { if (!abort.signal.aborted) setError(oauthErrorMessage(value, t)) })
    void apiRequest<{ installation?: InstalledConsent | null }>(`/api/oauth/installation?workspace=${workspace}&client_id=${encodeURIComponent(clientId)}`, { signal: abort.signal })
      .then(value => { if (!abort.signal.aborted) setInstalled(value?.installation ?? null) })
      // Members cannot install: the install request itself reports the error.
      .catch(() => { if (!abort.signal.aborted) setInstalled(null) })
    return () => abort.abort()
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isApplication, workspaceKey, clientId])

  const needsTeams = isApplication && !installed && teamMode !== 'public' && !teamIds.length
  const submit = async (approve: boolean, workspaceOverride?: string) => {
    const targetWorkspace = workspaceOverride ?? workspaceKey
    if (pendingRef.current || (approve && (!targetWorkspace || !workspaceOverride && needsTeams))) return
    pendingRef.current = true
    setPending(true)
    setError('')
    try {
      const result = await decideOAuthAuthorization({
        actor: query.get('actor') ?? undefined,
        // An installed application keeps its team access: only a first install sends it.
        teamIds: isApplication && !installed ? (teamMode === 'public' ? [] : teamIds) : undefined,
        allPublicTeams: isApplication && !installed ? teamMode !== 'selected' : undefined,
        clientId: query.get('client_id') ?? '',
        redirectUri: query.get('redirect_uri') ?? '',
        responseType: query.get('response_type') ?? '',
        scope: query.get('scope') ?? '',
        state: query.get('state') ?? '',
        codeChallenge: query.get('code_challenge') ?? '',
        codeChallengeMethod: query.get('code_challenge_method') ?? '',
        resource: query.get('resource') ?? undefined,
        workspaceKey: targetWorkspace,
        approve,
      })
      if (activeRef.current) window.location.assign(result.redirect)
    } catch (value) {
      if (!activeRef.current) return
      pendingRef.current = false
      setError(oauthErrorMessage(value, t))
      setPending(false)
      setAutoApproving(false)
    }
  }
  submitRef.current = submit

  const memberships = request?.workspaces.length ? request.workspaces : account.workspaces
  const selected = memberships.find(item => item.workspace.urlKey === workspaceKey)
    ?? account.workspaces.find(item => item.workspace.urlKey === account.lastWorkspaceKey)
    ?? memberships[0]
  const switcher = <OAuthWorkspaceSwitcher email={request?.viewer.email ?? account.viewer.email} memberships={memberships} selected={selected} onSelect={key => { setError(''); setWorkspaceKey(key) }} onCreate={() => navigate(workspaceOnboardingPath())} t={t}/>

  if (loadError) return <OAuthShell switcher={switcher}>
    <section className="oauth-error-page" role="alert">
      <FlowLogo className="oauth-error-logo" size={84}/>
      <p>{loadError}</p>
    </section>
  </OAuthShell>

  if (!request || autoApproving || isApplication && workspaceKey && installed === undefined) return <OAuthShell switcher={switcher}>
    <div aria-label={t('Loading authorization request')} className="oauth-loading"><GridLoader size={22}/></div>
  </OAuthShell>

  const appName = request.client.client_name
  const workspaceName = selected?.workspace.name ?? ''
  const permissions = consentPermissions(request.scopes, request.scopeLabels)
  if (request.realtime) permissions.push({ scope: REALTIME_PERMISSION, label: OAUTH_SCOPE_LABELS[REALTIME_PERMISSION] })
  const reconsent = isApplication && installed ? reconsentPermissions(installed, request.scopes, Boolean(request.realtime)) : undefined
  const alreadyInstalled = Boolean(reconsent && !reconsent.requested.length)
  const permissionList = (items: { scope: string; label: string }[]) => <ul className="oauth-list">
    {items.map(item => <li key={item.scope}><ScopeIcon scope={item.scope}/><span data-i18n-ignore>{t(item.label)}</span></li>)}
  </ul>
  return <OAuthShell switcher={switcher}>
    <section className="oauth-card" aria-labelledby="oauth-card-title">
      <header className="oauth-card-header">
        <OAuthMarks clientName={appName} logoUri={request.client.logo_uri} t={t}/>
        <div className="oauth-card-heading">
          {alreadyInstalled && installed ? <>
            <h1 id="oauth-card-title" data-i18n-ignore>{fill(t('{app} already installed'), { app: appName })}</h1>
            <p data-i18n-ignore>{fill(t('Installed {date} by {member}'), { date: installedDate(installed.createdAt), member: <strong key="member">{installed.installerName || t('a member')}</strong> })}</p>
          </> : <>
            <h1 id="oauth-card-title" data-i18n-ignore>{fill(t('{app} is requesting access'), { app: appName })}</h1>
            <p data-i18n-ignore>{fill(t('Authenticating with your {workspace} workspace'), { workspace: <strong key="workspace">{workspaceName}</strong> })}</p>
          </>}
        </div>
      </header>
      {reconsent ? <>
        <div className="oauth-card-section">
          <h2>{t('Existing permissions')}</h2>
          {permissionList(reconsent.existing)}
        </div>
        {reconsent.requested.length > 0 && <div className="oauth-card-section">
          <h2>{t('Requested permissions')}</h2>
          {permissionList(reconsent.requested)}
        </div>}
      </> : <div className="oauth-card-section">
        <h2>{t('Permissions')}</h2>
        {permissionList(permissions)}
      </div>}
      {isApplication && !installed && <ConsentTeamAccess teams={teams.filter(team => !team.retiredAt && !team.archivedAt)} mode={teamMode} teamIds={teamIds} t={t} onChange={(nextMode, nextIds) => { setTeamMode(nextMode); setTeamIds(nextIds) }}/>}
      {error && <p className="oauth-card-error" role="alert">{error}</p>}
      <footer className="oauth-card-actions">
        <button type="button" className="oauth-button" disabled={pending} onClick={() => void submit(false)}>{t('Cancel')}</button>
        <button type="button" className="oauth-button is-primary" disabled={pending || needsTeams || !workspaceKey} aria-busy={pending || undefined} onClick={() => void submit(true)}>{t(alreadyInstalled ? 'Continue' : reconsent ? 'Update access' : 'Authorize')}</button>
      </footer>
    </section>
    {!reconsent && <div className="oauth-notice" data-i18n-ignore>
      <span>{fill(t('This application is requesting access to your Flow workspace. By approving, you authorize Flow to share this data with {app} on behalf of your organization. Once shared, {app}’s use of the data is governed by its own terms and privacy policy, not by Flow’s terms or data commitments.'), { app: appName })}</span>
      <span>{fill(t('Owners and admins can revoke this connection at any time in Settings. Learn about {link}.'), { link: <a key="link" href={settingsPath(workspaceKey || selected?.workspace.urlKey || '', 'applications')} onClick={event => { event.preventDefault(); navigate(settingsPath(workspaceKey || selected?.workspace.urlKey || '', 'applications')) }}>{t('third-party applications')}</a> })}</span>
    </div>}
  </OAuthShell>
}

/** The reference app's "Installed Oct 14" date: short month and day in the page's locale. */
function installedDate(value: string) {
  return new Intl.DateTimeFormat(document.documentElement.dataset.locale || 'en-US', { month: 'short', day: 'numeric' }).format(new Date(value))
}

function OAuthShell({ switcher, children }: { switcher: ReactNode; children: ReactNode }) {
  return <main className="oauth-page">
    {switcher}
    <div className="oauth-page-body">{children}</div>
  </main>
}

/** The reference app's corner workspace menu: the signed-in email, the workspaces (check on the one being authorized) and Account actions. */
function OAuthWorkspaceSwitcher({ email, memberships, selected, onSelect, onCreate, t }: { email: string; memberships: WorkspaceMembership[]; selected?: WorkspaceMembership; onSelect: (key: string) => void; onCreate: () => void; t: Translate }) {
  if (!selected) return null
  return <DropdownMenu>
    <DropdownMenuTrigger className="oauth-workspace-trigger" aria-label={t('Switch workspace')}>
      <WorkspaceAvatar workspace={selected.workspace}/>
      <span data-i18n-ignore>{selected.workspace.name}</span>
      <ChevronDown aria-hidden="true"/>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" sideOffset={4} className="oauth-workspace-menu">
      <DropdownMenuLabel data-i18n-ignore>{email}</DropdownMenuLabel>
      {memberships.map((membership, index) => <DropdownMenuItem key={membership.workspace.id} onSelect={() => onSelect(membership.workspace.urlKey)} className={membership.workspace.id === selected.workspace.id ? 'is-current' : undefined}>
        <WorkspaceAvatar workspace={membership.workspace}/>
        <span className="oauth-workspace-option" data-i18n-ignore>{membership.workspace.name}</span>
        {membership.workspace.id === selected.workspace.id && <Check aria-hidden="true" className="oauth-workspace-check"/>}
        {index < 9 && <kbd className="oauth-workspace-index">{index + 1}</kbd>}
      </DropdownMenuItem>)}
      <DropdownMenuLabel>{t('Account')}</DropdownMenuLabel>
      <DropdownMenuItem onSelect={onCreate}>{t('Create or join a workspace…')}</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
}

function OAuthMarks({ clientName, logoUri, t }: { clientName: string; logoUri?: string; t: Translate }) {
  const logo = safeHttpUrl(logoUri)
  return <div className="oauth-marks">
    <span className={`oauth-mark${logo ? '' : ' is-placeholder'}`} role="img" aria-label={t('{app} icon').replace('{app}', clientName)}>
      {logo ? <img src={logo} alt=""/> : <ApplicationIconPlaceholder/>}
    </span>
    <ArrowRightLeft aria-hidden="true" className="oauth-marks-arrows"/>
    <span className="oauth-mark is-flow" role="img" aria-label="Flow"><FlowLogo size={34}/></span>
  </div>
}

/**
 * The reference app's app-user team access on the consent screen: "All public teams", "All public teams and select other
 * teams…" (offered when the workspace has a private team; its picker lists the private teams) and "Only select
 * teams…" (every team). A picker sits under its own option.
 */
function ConsentTeamAccess({ teams, mode, teamIds, onChange, t }: { teams: Team[]; mode: TeamMode; teamIds: string[]; onChange: (mode: TeamMode, teamIds: string[]) => void; t: Translate }) {
  const privateTeams = teams.filter(team => team.private)
  const options: [TeamMode, string][] = [['public', 'All public teams'], ...(privateTeams.length ? [['mixed', 'All public teams and select other teams…'] as [TeamMode, string]] : []), ['selected', 'Only select teams…']]
  return <div className="oauth-card-section">
    <h2>{t('Team access')}</h2>
    <div className="oauth-list oauth-team-access" role="radiogroup" aria-label={t('Team access')}>
      {options.map(([value, label]) => <div className="oauth-team-option" key={value}>
        <label><input type="radio" name="oauth-team-access" checked={mode === value} onChange={() => onChange(value, nextTeamSelection(value, teamIds, teams))}/><span>{t(label)}</span></label>
        {mode === value && value !== 'public' && <TeamCombobox teams={value === 'mixed' ? privateTeams : teams} value={teamIds} onChange={next => onChange(value, next)} placeholder={value === 'mixed' ? 'Select other teams…' : 'Select teams…'} t={t}/>}
      </div>)}
    </div>
  </div>
}

/** Fills `{name}` placeholders with text or elements (so translated sentences keep their own word order). */
function fill(template: string, values: Record<string, ReactNode>): ReactNode {
  const parts = template.split(/(\{\w+\})/g)
  if (parts.every(part => !/^\{\w+\}$/.test(part) || typeof values[part.slice(1, -1)] === 'string')) {
    return parts.map(part => /^\{\w+\}$/.test(part) ? values[part.slice(1, -1)] as string : part).join('')
  }
  return parts.map((part, index) => /^\{\w+\}$/.test(part) ? <Fragment key={index}>{values[part.slice(1, -1)]}</Fragment> : part)
}

/** Only http(s) URLs from the OAuth client are loaded as images. */
function safeHttpUrl(value?: string) { if (!value) return undefined; try { const url = new URL(value); return url.protocol === 'https:' || url.protocol === 'http:' ? url.toString() : undefined } catch { return undefined } }
