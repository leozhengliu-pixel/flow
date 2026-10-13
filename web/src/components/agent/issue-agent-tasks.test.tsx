import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap,makeIssue,viewer } from '@/test/fixtures'
import { setRuntimePreferences } from '@/lib/runtime-preferences'
import { AGENT_TASK_ACTIVITY_EVENT,canDelegateTo,listApplicationTasks,getApplicationTask,reactApplicationTask,replyApplicationTask,deleteApplicationActivity,type ApplicationTask } from '@/lib/application-agents'
import { IssueAgentTasks,IssueAgentTasksProvider } from './issue-agent-tasks'

vi.mock('@/lib/application-agents',async original=>({...await original<typeof import('@/lib/application-agents')>(),listApplicationTasks:vi.fn(),getApplicationTask:vi.fn(),replyApplicationTask:vi.fn(),reactApplicationTask:vi.fn(),deleteApplicationActivity:vi.fn()}))
vi.mock('./agent-rich-text',()=>({AgentRichText:({content}:{content:string})=><div>{content}</div>}))
const app={...viewer,id:'app-one',displayName:'Agent',app:true,appScopes:['app:assignable'],appTeamIds:['team-1']}
const task:ApplicationTask={id:'session',issueId:'issue-1',teamId:'team-1',appUserId:app.id,creatorId:viewer.id,status:'awaitingInput',version:3,prompt:'Inspect',trigger:'delegation',updatedAt:'',pendingTool:{name:'save_issue',arguments:{title:'Changed'},status:'pending'}}
afterEach(()=>setRuntimePreferences({}))
beforeEach(()=>{vi.clearAllMocks();vi.mocked(listApplicationTasks).mockResolvedValue([task]);vi.mocked(getApplicationTask).mockResolvedValue({session:task,activities:[]});vi.mocked(replyApplicationTask).mockResolvedValue({...task,status:'pending'})})
describe('issue agent tasks',()=>{
  it('gates delegates by application capabilities and selected teams',()=>{
    expect(canDelegateTo(app,'team-1')).toBe(true)
    expect(canDelegateTo(app,'other')).toBe(false)
    expect(canDelegateTo({...app,active:false},'team-1')).toBe(false)
    expect(canDelegateTo(viewer,'team-1')).toBe(false)
  })
  it('submits an explicit approval with the current task version',async()=>{
    const user=userEvent.setup(),issue=makeIssue({delegate:app})
    render(<I18nProvider><IssueAgentTasks issue={issue} data={makeBootstrap({users:[viewer,app]})}/></I18nProvider>)
    await user.click(await screen.findByRole('button',{name:'Approve'}))
    await waitFor(()=>expect(replyApplicationTask).toHaveBeenCalledWith('workspace',task,'prompt','',true))
  })
  it('sends a picked option at once and collapses the options into the chosen one',async()=>{
    const user=userEvent.setup(),waiting={...task,pendingTool:undefined,commentId:'comment-one'}
    vi.mocked(listApplicationTasks).mockResolvedValue([waiting])
    vi.mocked(getApplicationTask).mockResolvedValue({session:waiting,activities:[{id:'q',sessionId:task.id,actorId:app.id,type:'elicitation',body:'Language?',createdAt:'',signal:'select',signalMetadata:{options:[{value:'English'},{value:'Chinese'}]}}]})
    const issue=makeIssue({delegate:app}),data=makeBootstrap({users:[viewer,app]})
    render(<I18nProvider><IssueAgentTasksProvider issue={issue} data={data}><IssueAgentTasks issue={issue} data={data} commentId="comment-one"/></IssueAgentTasksProvider></I18nProvider>)
    expect(await screen.findByRole('group',{name:'Choose an option'})).toBeVisible()
    await user.click(screen.getByRole('button',{name:'Chinese'}))
    await waitFor(()=>expect(replyApplicationTask).toHaveBeenCalledWith('workspace',waiting,'prompt','Chinese',undefined))
    expect(listApplicationTasks).toHaveBeenCalledTimes(2)
  })
  it('allows a follow-up after a completed response',async()=>{
    const user=userEvent.setup(),completed={...task,status:'complete' as const,pendingTool:undefined}
    vi.mocked(listApplicationTasks).mockResolvedValue([completed])
    vi.mocked(getApplicationTask).mockResolvedValue({session:completed,activities:[{id:'r',sessionId:task.id,actorId:app.id,type:'response',body:'Done',createdAt:''}]})
    render(<I18nProvider><IssueAgentTasks issue={makeIssue({delegate:app})} data={makeBootstrap({users:[viewer,app]})}/></I18nProvider>)
    await user.click(await screen.findByRole('button',{name:/Leave a reply/}))
    await user.type(screen.getByRole('textbox',{name:'Reply to agent'}),'Explain further')
    await user.click(screen.getByRole('button',{name:'Send reply'}))
    await waitFor(()=>expect(replyApplicationTask).toHaveBeenCalledWith('workspace',completed,'prompt','Explain further',undefined))
  })
  it('preserves Chinese composition and honors the comment submit preference',async()=>{
    const user=userEvent.setup(),completed={...task,status:'complete' as const,pendingTool:undefined}
    vi.mocked(listApplicationTasks).mockResolvedValue([completed])
    vi.mocked(getApplicationTask).mockResolvedValue({session:completed,activities:[]})
    render(<I18nProvider><IssueAgentTasks issue={makeIssue({delegate:app})} data={makeBootstrap({users:[viewer,app]})}/></I18nProvider>)
    await user.click(await screen.findByRole('button',{name:/Leave a reply/}))
    const input=screen.getByRole('textbox',{name:'Reply to agent'})
    await user.type(input,'中文')
    fireEvent.keyDown(input,{key:'Enter',isComposing:true})
    expect(replyApplicationTask).not.toHaveBeenCalled()
    setRuntimePreferences({sendComments:'⌘ Enter'})
    fireEvent.keyDown(input,{key:'Enter'})
    expect(replyApplicationTask).not.toHaveBeenCalled()
    fireEvent.keyDown(input,{key:'Enter',ctrlKey:true})
    await waitFor(()=>expect(replyApplicationTask).toHaveBeenCalledWith('workspace',completed,'prompt','中文',undefined))
  })
  it('saves a reaction without resuming the completed task',async()=>{
    const user=userEvent.setup(),completed={...task,status:'complete' as const,pendingTool:undefined}
    vi.mocked(listApplicationTasks).mockResolvedValue([completed])
    vi.mocked(getApplicationTask).mockResolvedValue({session:completed,activities:[{id:'r',sessionId:task.id,actorId:app.id,type:'response',body:'Answer',createdAt:''}]})
    vi.mocked(reactApplicationTask).mockResolvedValue({...completed,version:4})
    render(<I18nProvider><IssueAgentTasks issue={makeIssue({delegate:app})} data={makeBootstrap({users:[viewer,app]})}/></I18nProvider>)
    await user.click(await screen.findByRole('button',{name:/React with thumbs up/}))
    await waitFor(()=>expect(reactApplicationTask).toHaveBeenCalledWith('workspace',completed,'r','👍',true))
    expect(replyApplicationTask).not.toHaveBeenCalled()
    expect(await screen.findByRole('button',{name:/React with thumbs up/})).toHaveAttribute('aria-pressed','true')
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
  it('shows no thinking or steps in a comment thread, only the reference app\'s "<Agent> Working…" row with the current phase',async()=>{
    const working={...task,status:'active' as const,pendingTool:undefined,commentId:'comment-one'}
    vi.mocked(listApplicationTasks).mockResolvedValue([working])
    vi.mocked(getApplicationTask).mockResolvedValue({session:working,activities:[{id:'t',sessionId:task.id,actorId:app.id,type:'thought',body:'Secret plan',createdAt:''},{id:'a',sessionId:task.id,actorId:app.id,type:'action',body:'',metadata:{action:'Looking at issues'},createdAt:''},{id:'o',sessionId:task.id,actorId:app.id,type:'output',body:'Half an answ',createdAt:''}]})
    const issue=makeIssue({delegate:app}),data=makeBootstrap({users:[viewer,app]})
    render(<I18nProvider><IssueAgentTasksProvider issue={issue} data={data}><IssueAgentTasks issue={issue} data={data} commentId="comment-one"/></IssueAgentTasksProvider></I18nProvider>)
    const label=await screen.findByText('Looking at issues…',{selector:'.issue-agent-working-text'},{timeout:4000})
    const row=label.closest('[role=status]')!
    expect(row).toHaveTextContent('Agent')
    expect(screen.queryByText('Secret plan')).not.toBeInTheDocument()
    expect(screen.queryByText('Half an answ')).not.toBeInTheDocument()
    expect(screen.queryByRole('button',{name:'Stop agent'})).not.toBeInTheDocument()
  })
  it('offers the reference app\'s comment menu on an agent reply and deletes it',async()=>{
    const user=userEvent.setup(),done={...task,status:'complete' as const,pendingTool:undefined,commentId:'comment-one'}
    vi.mocked(listApplicationTasks).mockResolvedValue([done])
    vi.mocked(getApplicationTask).mockResolvedValue({session:done,activities:[{id:'r',sessionId:task.id,actorId:app.id,type:'response',body:'Done',createdAt:''}]})
    vi.mocked(deleteApplicationActivity).mockResolvedValue({...done,version:4})
    const issue=makeIssue({delegate:app}),data=makeBootstrap({users:[viewer,app]})
    render(<I18nProvider><IssueAgentTasksProvider issue={issue} data={data}><IssueAgentTasks issue={issue} data={data} commentId="comment-one" threadControls={{following:true,onToggleSubscription:vi.fn(),resolved:false,onResolve:vi.fn(),parentIssueId:'issue-1'}}/></IssueAgentTasksProvider></I18nProvider>)
    await user.click(await screen.findByRole('button',{name:'Comment options'}))
    expect((await screen.findAllByRole('menuitem')).map(item=>item.textContent)).toEqual(['Unsubscribe from thread','Resolve thread with comment','Copy link to comment','Copy content as Markdown','New issue from comment…','New sub-issue from comment…','Delete'])
    await user.click(screen.getByRole('menuitem',{name:'Delete'}))
    await waitFor(()=>expect(deleteApplicationActivity).toHaveBeenCalledWith('workspace',done,'r'))
    await waitFor(()=>expect(screen.queryByText('Done')).not.toBeInTheDocument())
  })
})
