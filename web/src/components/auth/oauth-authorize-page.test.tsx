import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useNavigate } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { OAuthAuthorizePage } from './oauth-authorize-page'
import { decideOAuthAuthorization, fetchOAuthAuthorizationRequest, fetchUserSettings, type OAuthAuthorizationRequest } from '@/lib/api'
import type { AccountBootstrap } from '@/types/flow'
import { viewer } from '@/test/fixtures'

vi.mock('@/lib/api',()=>({decideOAuthAuthorization:vi.fn(),fetchOAuthAuthorizationRequest:vi.fn(),fetchUserSettings:vi.fn()}))
vi.mock('@/lib/theme',()=>({applyTheme:vi.fn()}))
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
})
