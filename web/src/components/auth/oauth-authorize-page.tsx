import { useEffect, useMemo, useState } from 'react'
import { ArrowLeftRight, Cable, ChevronDown, LoaderCircle, ShieldCheck } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'

import { decideOAuthAuthorization, fetchOAuthAuthorizationRequest, fetchUserSettings, type OAuthAuthorizationRequest } from '@/lib/api'
import { applyTheme } from '@/lib/theme'
import type { AccountBootstrap } from '@/types/flow'

import './oauth-authorize-page.css'

type Props = { account: AccountBootstrap }

export function OAuthAuthorizePage({ account }: Props) {
  const location = useLocation()
  const navigate = useNavigate()
  const query = useMemo(() => new URLSearchParams(location.search), [location.search])
  const [request, setRequest] = useState<OAuthAuthorizationRequest>()
  const [workspaceKey, setWorkspaceKey] = useState('')
  const [selecting, setSelecting] = useState(true)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const key = account.workspaces.find(item => item.workspace.urlKey === account.lastWorkspaceKey)?.workspace.urlKey ?? account.workspaces[0]?.workspace.urlKey
    if (key) void fetchUserSettings(key).then(applyTheme).catch(() => undefined)
  }, [account.lastWorkspaceKey, account.workspaces])

  useEffect(() => {
    fetchOAuthAuthorizationRequest(location.search)
      .then(value => {
        setRequest(value)
        const preferred = value.workspaces.find(item => item.workspace.urlKey === account.lastWorkspaceKey) ?? value.workspaces[0]
        if (value.workspaces.length === 1 && preferred) {
          setWorkspaceKey(preferred.workspace.urlKey)
          setSelecting(false)
        }
      })
      .catch(value => setError(value instanceof Error ? value.message : 'This authorization request is invalid.'))
  }, [account.lastWorkspaceKey, location.search])

  const selected = request?.workspaces.find(item => item.workspace.urlKey === workspaceKey)
  const submit = async (approve: boolean) => {
    if (approve && !workspaceKey) return
    setPending(true)
    setError('')
    try {
      const result = await decideOAuthAuthorization({
        clientId: query.get('client_id') ?? '',
        redirectUri: query.get('redirect_uri') ?? '',
        responseType: query.get('response_type') ?? '',
        scope: query.get('scope') ?? '',
        state: query.get('state') ?? '',
        codeChallenge: query.get('code_challenge') ?? '',
        codeChallengeMethod: query.get('code_challenge_method') ?? '',
        resource: query.get('resource') ?? undefined,
        workspaceKey,
        approve,
      })
      window.location.assign(result.redirect)
    } catch (value) {
      setError(value instanceof Error ? value.message : 'Could not complete authorization.')
      setPending(false)
    }
  }

  if (!request) return <OAuthShell workspaceName={account.workspaces[0]?.workspace.name}>
    <div className="oauth-loading">{error || <LoaderCircle/>}</div>
  </OAuthShell>

  if (selecting) return <OAuthShell workspaceName={account.workspaces.find(item => item.workspace.urlKey === account.lastWorkspaceKey)?.workspace.name}>
    <section className="oauth-workspace-card">
      <OAuthMarks clientName={request.client.client_name}/>
      <h1>{request.client.client_name} is requesting access</h1>
      <p>Select a workspace to authenticate</p>
      <label>Workspace to connect</label>
      <div className="oauth-workspace-list">
        {request.workspaces.map(item => <button key={item.workspace.id} onClick={() => {
          setWorkspaceKey(item.workspace.urlKey)
          setSelecting(false)
        }}>
          <WorkspaceMark name={item.workspace.name} color={item.workspace.color}/>
          <span><strong data-i18n-ignore>{item.workspace.name}</strong><small data-i18n-ignore>{request.viewer.email}</small></span>
          <ChevronDown/>
        </button>)}
      </div>
      {error && <div className="oauth-error">{error}</div>}
    </section>
  </OAuthShell>

  return <OAuthShell workspaceName={selected?.workspace.name} onWorkspace={() => setSelecting(true)}>
    <section className="oauth-consent-card">
      <OAuthMarks clientName={request.client.client_name}/>
      <h1>{request.client.client_name} is requesting access</h1>
      <div className="oauth-details">
        <h2>Details</h2>
        <dl>
          <div><dt>Name:</dt><dd data-i18n-ignore>{request.client.client_name}</dd></div>
          <div><dt>Redirect URIs:</dt><dd data-i18n-ignore>{request.redirectUri}</dd></div>
          <div><dt>Access:</dt><dd>{request.scopeLabels.join(', ')}</dd></div>
        </dl>
      </div>
      <div className="oauth-copy">
        <p>This MCP client is requesting access to your Flow workspace. By approving, you authorize Flow to share this data with <strong data-i18n-ignore>{request.client.client_name}</strong> on behalf of your organization. Once shared, the client's use of the data is governed by its own terms and privacy policy, not by Flow's terms or data commitments.</p>
        <p>Owners and admins can revoke this connection at any time in Settings. Learn about <button onClick={() => navigate(`/${workspaceKey}/settings/applications`)}>third-party applications</button>.</p>
      </div>
      {error && <div className="oauth-error">{error}</div>}
      <footer>
        <button className="oauth-cancel" disabled={pending} onClick={() => void submit(false)}>Cancel</button>
        <button className="oauth-approve" disabled={pending} onClick={() => void submit(true)}>{pending ? <LoaderCircle/> : <ShieldCheck/>}Approve</button>
      </footer>
    </section>
  </OAuthShell>
}

function OAuthShell({ workspaceName, onWorkspace, children }: { workspaceName?: string; onWorkspace?: () => void; children: React.ReactNode }) {
  return <main className="oauth-page">
    <header><button className="oauth-workspace-menu" onClick={onWorkspace} disabled={!onWorkspace}><WorkspaceMark name={workspaceName ?? 'Flow'}/><span data-i18n-ignore>{workspaceName ?? 'Flow'}</span>{onWorkspace && <ChevronDown/>}</button></header>
    <div className="oauth-page-body">{children}</div>
  </main>
}

function OAuthMarks({ clientName }: { clientName: string }) {
  return <div className="oauth-marks"><span className="oauth-client-mark" aria-label="MCP client"><Cable/></span><ArrowLeftRight/><span className="oauth-flow-mark" aria-label="Flow"><i/><i/><i/></span><span className="oauth-client-name" data-i18n-ignore>{clientName}</span></div>
}

function WorkspaceMark({ name, color }: { name: string; color?: string }) {
  const initials = name.split(/\s+/).map(part => part[0]).join('').slice(0, 2).toUpperCase() || 'F'
  return <span className="oauth-workspace-mark" style={color ? { backgroundColor: color } : undefined}>{initials}</span>
}
