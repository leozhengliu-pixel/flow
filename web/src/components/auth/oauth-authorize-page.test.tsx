import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useNavigate } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { OAuthAuthorizePage } from './oauth-authorize-page'
import { decideOAuthAuthorization, fetchOAuthAuthorizationRequest, fetchUserSettings, type OAuthAuthorizationRequest } from '@/lib/api'
import type { AccountBootstrap } from '@/types/flow'
import { viewer } from '@/test/fixtures'
import { I18nProvider } from '@/i18n/i18n'

vi.mock('@/lib/api',()=>({decideOAuthAuthorization:vi.fn(),fetchOAuthAuthorizationRequest:vi.fn(),fetchUserSettings:vi.fn()}))
vi.mock('@/lib/api-client',async importOriginal=>({...(await importOriginal<typeof import('@/lib/api-client')>()),request:vi.fn().mockResolvedValue([])}))
vi.mock('@/lib/theme',()=>({applyAccountTheme:vi.fn(),applyTheme:vi.fn()}))
const account:AccountBootstrap={viewer,workspaces:[{workspace:{id:'workspace-1',name:'Workspace',urlKey:'workspace'},role:'Admin',joinedAt:'2026-09-11T00:00:00Z',issueCount:75675}],lastWorkspaceKey:'workspace',workspaceRegionSelectorEnabled:false,workspaceDefaultRegion:'global'}
const authorization:OAuthAuthorizationRequest={client:{client_id:'client-1',client_name:'First client'},redirectUri:'http://127.0.0.1/callback',scopes:['read'],scopeLabels:['Read workspace'],workspaces:account.workspaces,viewer}
const initial='/oauth/authorize?client_id=client-1&redirect_uri=http%3A%2F%2F127.0.0.1%2Fcallback&response_type=code&code_challenge=challenge&code_challenge_method=S256'
function Navigation(){const navigate=useNavigate();return <button type="button" onClick={()=>navigate('/oauth/authorize?client_id=client-2')}>Change client</button>}
function App({value=account}:{value?:AccountBootstrap}){return <MemoryRouter initialEntries={[initial]}><OAuthAuthorizePage account={value}/><Navigation/></MemoryRouter>}

beforeEach(()=>{vi.clearAllMocks();vi.mocked(fetchUserSettings).mockImplementation(()=>new Promise(()=>{}));vi.mocked(fetchOAuthAuthorizationRequest).mockResolvedValue(authorization)})
describe('OAuth consent request lifecycle',()=>{
  it('renders approval without awaiting theme settings and avoids reference-triggered duplicate reads',async()=>{
    const view=render(<App/>)
    await screen.findByRole('button',{name:'Approve'})
    expect(fetchOAuthAuthorizationRequest).toHaveBeenCalledTimes(1)
    expect(fetchUserSettings).toHaveBeenCalledTimes(1)
    view.rerender(<App value={{...account,workspaces:[...account.workspaces],lastWorkspaceKey:undefined}}/>)
    expect(fetchOAuthAuthorizationRequest).toHaveBeenCalledTimes(1)
    expect(fetchUserSettings).toHaveBeenCalledTimes(1)
  })

  it('cancels previous authorization reads and ignores stale responses after switching requests',async()=>{
    let finish!: (value:OAuthAuthorizationRequest)=>void
    vi.mocked(fetchOAuthAuthorizationRequest).mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}))
    vi.mocked(fetchOAuthAuthorizationRequest).mockResolvedValueOnce({...authorization,client:{client_id:'client-2',client_name:'Second client'}})
    const view=render(<App/>)
    await waitFor(()=>expect(fetchOAuthAuthorizationRequest).toHaveBeenCalledTimes(1))
    const oldSignal=vi.mocked(fetchOAuthAuthorizationRequest).mock.calls[0][1]!
    fireEvent.click(screen.getByRole('button',{name:'Change client'}))
    await screen.findByRole('heading',{name:'Second client is requesting access'})
    expect(oldSignal.aborted).toBe(true)
    await act(async()=>finish(authorization))
    expect(screen.queryByRole('heading',{name:'First client is requesting access'})).not.toBeInTheDocument()
    const lastSignal=vi.mocked(fetchOAuthAuthorizationRequest).mock.calls.at(-1)![1]!
    view.unmount()
    expect(lastSignal.aborted).toBe(true)
  })

  it('submits one authorization for rapid clicks and allows retry after a failed request',async()=>{
    let fail!: (reason:Error)=>void
    vi.mocked(decideOAuthAuthorization).mockImplementationOnce(()=>new Promise((_resolve,reject)=>{fail=reject})).mockImplementation(()=>new Promise(()=>{}))
    render(<App/>)
    const approve=await screen.findByRole('button',{name:'Approve'})
    act(()=>{approve.click();approve.click()})
    expect(decideOAuthAuthorization).toHaveBeenCalledTimes(1)
    expect(approve).toBeDisabled()
    await act(async()=>fail(new Error('Temporarily unavailable')))
    expect(screen.getByText('Temporarily unavailable')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button',{name:'Approve'}))
    expect(decideOAuthAuthorization).toHaveBeenCalledTimes(2)
  })

  it('shows the server authorization error instead of a generic expired-link message',async()=>{
    vi.mocked(fetchOAuthAuthorizationRequest).mockRejectedValueOnce(new Error('S256 PKCE code challenge is required'))
    render(<App/>)
    expect(await screen.findByText('S256 PKCE code challenge is required')).toBeInTheDocument()
    expect(screen.queryByText('This authorization link is incomplete or has expired.')).not.toBeInTheDocument()
  })
  it('approves straight away for a workspace the user already authorized, without showing consent',async()=>{
    vi.mocked(fetchOAuthAuthorizationRequest).mockResolvedValue({...authorization,previouslyAuthorizedWorkspaceKey:'workspace',prompt:''})
    vi.mocked(decideOAuthAuthorization).mockImplementation(()=>new Promise(()=>{}))
    render(<App/>)
    await waitFor(()=>expect(decideOAuthAuthorization).toHaveBeenCalledTimes(1))
    expect(decideOAuthAuthorization).toHaveBeenCalledWith(expect.objectContaining({clientId:'client-1',workspaceKey:'workspace',approve:true}))
    expect(screen.queryByRole('button',{name:'Approve'})).not.toBeInTheDocument()
    expect(screen.getByLabelText('Loading authorization request')).toBeInTheDocument()
  })

  it('asks for consent when no previous authorization exists, and when consent is forced',async()=>{
    vi.mocked(fetchOAuthAuthorizationRequest).mockResolvedValue({...authorization,previouslyAuthorizedWorkspaceKey:''})
    const view=render(<App/>)
    await screen.findByRole('button',{name:'Approve'})
    expect(decideOAuthAuthorization).not.toHaveBeenCalled()
    view.unmount()
    vi.mocked(fetchOAuthAuthorizationRequest).mockResolvedValue({...authorization,previouslyAuthorizedWorkspaceKey:'workspace',prompt:'consent'})
    render(<App/>)
    await screen.findByRole('button',{name:'Approve'})
    expect(decideOAuthAuthorization).not.toHaveBeenCalled()
  })

  it('shows the app-install consent even if the workspace was authorized before',async()=>{
    vi.mocked(fetchOAuthAuthorizationRequest).mockResolvedValue({...authorization,actor:'app',previouslyAuthorizedWorkspaceKey:'workspace'})
    render(<I18nProvider><MemoryRouter initialEntries={[initial+'&actor=app']}><OAuthAuthorizePage account={account}/></MemoryRouter></I18nProvider>)
    await screen.findByRole('button',{name:'Install application'})
    expect(decideOAuthAuthorization).not.toHaveBeenCalled()
  })

  it('shows the client logo and developer link, and ignores non-http URLs',async()=>{
    vi.mocked(fetchOAuthAuthorizationRequest).mockResolvedValue({...authorization,client:{...authorization.client,logo_uri:'https://cdn.example.com/logo.png',client_uri:'https://example.com'}})
    const view=render(<App/>)
    await screen.findByRole('button',{name:'Approve'})
    expect(view.container.querySelector('img.oauth-client-logo')).toHaveAttribute('src','https://cdn.example.com/logo.png')
    expect(screen.getByRole('link',{name:'https://example.com'})).toHaveAttribute('href','https://example.com/')
    view.unmount()
    vi.mocked(fetchOAuthAuthorizationRequest).mockResolvedValue({...authorization,client:{...authorization.client,logo_uri:'javascript:alert(1)',client_uri:'javascript:alert(1)'}})
    const unsafe=render(<App/>)
    await screen.findByRole('button',{name:'Approve'})
    expect(unsafe.container.querySelector('img.oauth-client-logo')).toBeNull()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it.each([
    ['invalid_scope','This application requested a permission that Flow does not support.'],
    ['invalid_client','This application is not registered or is no longer available.'],
  ])('maps the %s error code to a friendly message',async(code,message)=>{
    vi.mocked(fetchOAuthAuthorizationRequest).mockRejectedValueOnce(Object.assign(new Error('raw description'),{code}))
    render(<App/>)
    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(screen.queryByText('raw description')).not.toBeInTheDocument()
  })
})
