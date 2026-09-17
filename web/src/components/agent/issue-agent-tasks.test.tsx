import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap,makeIssue,viewer } from '@/test/fixtures'
import { TooltipProvider } from '@/components/ui/tooltip'
import { canDelegateTo,listApplicationTasks,getApplicationTask,replyApplicationTask,type ApplicationTask } from '@/lib/application-agents'
import { IssueAgentTasks,IssueAgentPicker } from './issue-agent-tasks'

vi.mock('@/lib/application-agents',async original=>({...await original<typeof import('@/lib/application-agents')>(),listApplicationTasks:vi.fn(),getApplicationTask:vi.fn(),replyApplicationTask:vi.fn()}))
vi.mock('./agent-rich-text',()=>({AgentRichText:({content}:{content:string})=><div>{content}</div>}))
const app={...viewer,id:'app-one',displayName:'Agent',app:true,appScopes:['app:assignable'],appTeamIds:['team-1']}
const task:ApplicationTask={id:'session',issueId:'issue-1',teamId:'team-1',appUserId:app.id,creatorId:viewer.id,status:'awaitingInput',version:3,prompt:'Inspect',trigger:'delegation',updatedAt:'',pendingTool:{name:'save_issue',arguments:{title:'Changed'},status:'pending'}}
beforeEach(()=>{vi.clearAllMocks();vi.mocked(listApplicationTasks).mockResolvedValue([task]);vi.mocked(getApplicationTask).mockResolvedValue({session:task,activities:[]});vi.mocked(replyApplicationTask).mockResolvedValue({...task,status:'pending'})})
describe('issue agent tasks',()=>{
  it('gates delegates by application capabilities and selected teams',()=>{
    expect(canDelegateTo(app,'team-1')).toBe(true)
    expect(canDelegateTo(app,'other')).toBe(false)
    expect(canDelegateTo({...app,active:false},'team-1')).toBe(false)
    expect(canDelegateTo(viewer,'team-1')).toBe(false)
  })
  it('keeps delegation separate from the human assignee',async()=>{
    const user=userEvent.setup(),update=vi.fn()
    const issue=makeIssue(),data=makeBootstrap({users:[viewer,app]})
    render(<I18nProvider><TooltipProvider><IssueAgentPicker data={data} issue={issue} onUpdate={update}/></TooltipProvider></I18nProvider>)
    await user.click(screen.getByRole('combobox',{name:'Delegate to agent'}))
    await user.click(screen.getByRole('option',{name:/Agent/}))
    expect(update).toHaveBeenCalledWith({delegateId:'app-one'})
  })
  it('submits an explicit approval with the current task version',async()=>{
    const user=userEvent.setup(),issue=makeIssue({delegate:app})
    render(<I18nProvider><IssueAgentTasks issue={issue} data={makeBootstrap({users:[viewer,app]})}/></I18nProvider>)
    await user.click(await screen.findByRole('button',{name:'Approve'}))
    await waitFor(()=>expect(replyApplicationTask).toHaveBeenCalledWith('workspace',task,'prompt','',true))
  })
})
