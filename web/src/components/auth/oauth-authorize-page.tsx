import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowRightLeft, Check, ChevronDown } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'

import { decideOAuthAuthorization, fetchOAuthAuthorizationRequest, fetchUserSettings, type OAuthAuthorizationRequest } from '@/lib/api'
import { applyAccountTheme } from '@/lib/theme'
import { request as apiRequest } from '@/lib/api-client'
import { settingsPath, workspaceOnboardingPath } from '@/lib/app-routes'
import { useOptionalTranslate } from '@/i18n/i18n'
import { consentPermissions, oauthErrorMessage } from '@/lib/oauth-consent'
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
  const [allPublicTeams, setAllPublicTeams] = useState(true)
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

  useEffect(() => {
    setTeams([]); setTeamIds([]); setAllPublicTeams(true)
    if (!isApplication || !workspaceKey) return
    const abort = new AbortController()
    void apiRequest<Team[]>(`/api/oauth/installation-teams?workspace=${encodeURIComponent(workspaceKey)}`, { signal: abort.signal })
      .then(value => { if (!abort.signal.aborted) setTeams(value) })
      .catch(value => { if (!abort.signal.aborted) setError(oauthErrorMessage(value, t)) })
    return () => abort.abort()
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isApplication, workspaceKey])

  const submit = async (approve: boolean, workspaceOverride?: string) => {
    const targetWorkspace = workspaceOverride ?? workspaceKey
    if (pendingRef.current || (approve && (!targetWorkspace || !workspaceOverride && isApplication && !allPublicTeams && !teamIds.length))) return
    pendingRef.current = true
    setPending(true)
    setError('')
    try {
      const result = await decideOAuthAuthorization({
        actor: query.get('actor') ?? undefined,
        teamIds: isApplication ? (allPublicTeams ? [] : teamIds) : undefined,
        allPublicTeams: isApplication ? allPublicTeams : undefined,
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

  if (!request || autoApproving) return <OAuthShell switcher={switcher}>
    <div aria-label={t('Loading authorization request')} className="oauth-loading"><GridLoader size={22}/></div>
  </OAuthShell>

  const appName = request.client.client_name
  const workspaceName = selected?.workspace.name ?? ''
  const permissions = consentPermissions(request.scopes, request.scopeLabels)
  const blocked = isApplication && !allPublicTeams && !teamIds.length
  return <OAuthShell switcher={switcher}>
    <section className="oauth-card" aria-labelledby="oauth-card-title">
      <header className="oauth-card-header">
        <OAuthMarks clientName={appName} logoUri={request.client.logo_uri} t={t}/>
        <div className="oauth-card-heading">
          <h1 id="oauth-card-title" data-i18n-ignore>{fill(t('{app} is requesting access'), { app: appName })}</h1>
          <p data-i18n-ignore>{fill(t('Authenticating with your {workspace} workspace'), { workspace: <strong key="workspace">{workspaceName}</strong> })}</p>
        </div>
      </header>
      <div className="oauth-card-section">
        <h2>{t('Permissions')}</h2>
        <ul className="oauth-list">
          {permissions.map(item => <li key={item.scope}><ScopeIcon scope={item.scope}/><span data-i18n-ignore>{t(item.label)}</span></li>)}
        </ul>
      </div>
      {isApplication && <ConsentTeamAccess teams={teams} allPublicTeams={allPublicTeams} teamIds={teamIds} t={t} onChange={(nextAll, nextIds) => { setAllPublicTeams(nextAll); setTeamIds(nextIds) }}/>}
      {error && <p className="oauth-card-error" role="alert">{error}</p>}
      <footer className="oauth-card-actions">
        <button type="button" className="oauth-button" disabled={pending} onClick={() => void submit(false)}>{t('Cancel')}</button>
        <button type="button" className="oauth-button is-primary" disabled={pending || blocked || !workspaceKey} aria-busy={pending || undefined} onClick={() => void submit(true)}>{t('Authorize')}</button>
      </footer>
    </section>
    <div className="oauth-notice" data-i18n-ignore>
      <span>{fill(t('This application is requesting access to your Flow workspace. By approving, you authorize Flow to share this data with {app} on behalf of your organization. Once shared, {app}’s use of the data is governed by its own terms and privacy policy, not by Flow’s terms or data commitments.'), { app: appName })}</span>
      <span>{fill(t('Owners and admins can revoke this connection at any time in Settings. Learn about {link}.'), { link: <a key="link" href={settingsPath(workspaceKey || selected?.workspace.urlKey || '', 'applications')} onClick={event => { event.preventDefault(); navigate(settingsPath(workspaceKey || selected?.workspace.urlKey || '', 'applications')) }}>{t('third-party applications')}</a> })}</span>
    </div>
  </OAuthShell>
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

function ConsentTeamAccess({ teams, allPublicTeams, teamIds, onChange, t }: { teams: Team[]; allPublicTeams: boolean; teamIds: string[]; onChange: (allPublicTeams: boolean, teamIds: string[]) => void; t: Translate }) {
  return <div className="oauth-card-section">
    <h2>{t('Team access')}</h2>
    <div className="oauth-list oauth-team-access" role="radiogroup" aria-label={t('Team access')}>
      <label><input type="radio" name="oauth-team-access" checked={allPublicTeams} onChange={() => onChange(true, [])}/><span>{t('All public teams')}</span></label>
      <label><input type="radio" name="oauth-team-access" checked={!allPublicTeams} onChange={() => onChange(false, teamIds)}/><span>{t('Only select teams…')}</span></label>
      {!allPublicTeams && <TeamCombobox teams={teams.filter(team => !team.retiredAt)} value={teamIds} onChange={next => onChange(false, next)} t={t}/>}
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
