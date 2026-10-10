import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap,makeIssue,viewer } from '@/test/fixtures'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AGENT_TASK_ACTIVITY_EVENT,canDelegateTo,listApplicationTasks,getApplicationTask,replyApplicationTask,type ApplicationTask } from '@/lib/application-agents'
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
  describe('refresh',()=>{
    afterEach(()=>{vi.useRealTimers()})
    const renderTasks=async(status:ApplicationTask['status'])=>{
      vi.useFakeTimers()
      vi.mocked(listApplicationTasks).mockResolvedValue([{...task,status,pendingTool:undefined}])
      render(<I18nProvider><IssueAgentTasks issue={makeIssue({delegate:app})} data={makeBootstrap({users:[viewer,app]})}/></I18nProvider>)
      await act(()=>vi.advanceTimersByTimeAsync(0))
      expect(listApplicationTasks).toHaveBeenCalledTimes(1)
    }
    it('does not poll while no task is working and reloads on a push for its resource',async()=>{
      await renderTasks('complete')
      await act(()=>vi.advanceTimersByTimeAsync(60_000))
      expect(listApplicationTasks).toHaveBeenCalledTimes(1)
      await act(async()=>{window.dispatchEvent(new CustomEvent(AGENT_TASK_ACTIVITY_EVENT,{detail:'another-issue'}));await vi.advanceTimersByTimeAsync(0)})
      expect(listApplicationTasks).toHaveBeenCalledTimes(1)
      await act(async()=>{window.dispatchEvent(new CustomEvent(AGENT_TASK_ACTIVITY_EVENT,{detail:makeIssue().id}));await vi.advanceTimersByTimeAsync(0)})
      expect(listApplicationTasks).toHaveBeenCalledTimes(2)
    })
    it('keeps a slow fallback poll only while a task is working',async()=>{
      await renderTasks('active')
      await act(()=>vi.advanceTimersByTimeAsync(9_000))
      expect(listApplicationTasks).toHaveBeenCalledTimes(1)
      await act(()=>vi.advanceTimersByTimeAsync(1_000))
      expect(listApplicationTasks).toHaveBeenCalledTimes(2)
    })
  })
})
