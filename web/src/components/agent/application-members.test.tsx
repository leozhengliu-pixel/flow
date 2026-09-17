import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { ApplicationMembers } from './application-members'
import { listApplications, saveApplication } from '@/lib/application-agents'

vi.mock('@/lib/application-agents',()=>({listApplications:vi.fn(),saveApplication:vi.fn()}))
beforeEach(()=>{vi.clearAllMocks();vi.mocked(listApplications).mockResolvedValue([])})
describe('application member installation',()=>{
  it('requires an explicit team scope and refreshes after installing',async()=>{
    const user=userEvent.setup(),reload=vi.fn().mockResolvedValue(undefined),data=makeBootstrap({viewerRole:'admin'})
    vi.mocked(saveApplication).mockResolvedValue({application:{id:'installed',name:'Flow Agent',clientId:'builtin-flow-agent',userId:'app-1',workspaceKey:'workspace',installedBy:data.viewer.id,scopes:['read'],teamIds:['team-1'],builtin:true,active:true,createdAt:'',updatedAt:''},webhookSecret:'test-secret'})
    render(<I18nProvider><ApplicationMembers data={data} onReload={reload}/></I18nProvider>)
    await user.click(screen.getByRole('button',{name:'Applications'}))
    expect(screen.getByRole('button',{name:'Save'})).toBeDisabled()
    await user.click(screen.getByRole('checkbox',{name:'Test team'}))
    await user.click(screen.getByRole('button',{name:'Save'}))
    await waitFor(()=>expect(saveApplication).toHaveBeenCalledWith('workspace',expect.objectContaining({builtin:true,teamIds:['team-1'],active:true})))
    await waitFor(()=>expect(reload).toHaveBeenCalledOnce())
  })
  it('does not offer application installation to ordinary members',()=>{
    render(<I18nProvider><ApplicationMembers data={makeBootstrap({viewerRole:'member'})} onReload={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('button',{name:'Applications'})).toBeNull()
  })
})
