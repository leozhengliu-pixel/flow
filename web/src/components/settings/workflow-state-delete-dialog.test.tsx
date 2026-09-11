import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TeamWorkflowSettings } from './team-workflow-settings'
import { WorkflowStateDeleteDialog } from './workflow-state-delete-dialog'
import { deleteWorkflowState, fetchWorkflowStates, listIssueRecordGroups, updateWorkflowState } from '@/lib/api'
import { makeBootstrap, backlog, started, completed } from '@/test/fixtures'
import type { WorkflowState } from '@/types/flow'

vi.mock('@/lib/api',async importOriginal=>({...await importOriginal<typeof import('@/lib/api')>(),deleteWorkflowState:vi.fn(),fetchWorkflowStates:vi.fn(),listIssueRecordGroups:vi.fn(),updateWorkflowState:vi.fn()}))
const staged:WorkflowState={...started,id:'state-staged',name:'Staged',teamId:'team-1',position:2}
const stateList=[{...backlog,default:true,teamId:'team-1'},{...started,teamId:'team-1'},staged,{...completed,teamId:'team-1'},{...completed,id:'state-canceled',name:'Canceled',type:'canceled' as const,teamId:'team-1'},{...completed,id:'duplicate',name:'Duplicate',type:'canceled' as const,reserved:true,teamId:'team-1'}]
beforeEach(()=>{
  vi.clearAllMocks();localStorage.clear()
  vi.mocked(fetchWorkflowStates).mockResolvedValue(stateList)
  vi.mocked(listIssueRecordGroups).mockResolvedValue({groups:[{value:staged.id,count:75675}]})
  vi.mocked(deleteWorkflowState).mockResolvedValue(undefined)
  vi.mocked(updateWorkflowState).mockResolvedValue(stateList[0])
})
function setup(){const data=makeBootstrap({issues:[],issueCollectionPaged:true,states:stateList});render(<I18nProvider><TeamWorkflowSettings data={data} team={data.teams[0]} section="statuses" onNavigate={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)}
async function openDelete(){const title=await screen.findByText('Staged');const row=title.closest('.issue-state-row')!;fireEvent.click(within(row as HTMLElement).getByRole('button',{name:'Open menu'}));fireEvent.click(await screen.findByRole('option',{name:'Delete'}));return screen.findByRole('dialog',{name:'Delete issue status'})}
async function chooseReplacement(){fireEvent.click(screen.getByRole('combobox',{name:'Replacement status'}));fireEvent.click(await screen.findByRole('option',{name:'In progress'}))}

describe('workflow status deletion',()=>{
  it('uses grouped server counts in paged workspaces and requires an explicit replacement',async()=>{
    setup()
    await waitFor(()=>expect(listIssueRecordGroups).toHaveBeenCalledWith({teamId:'team-1',includeSubTeams:false,groupBy:'status',archived:'all'},expect.any(AbortSignal)))
    await screen.findByText(/75675/)
    await openDelete()
    expect(screen.getByText('75,675 issues will be moved.')).toBeInTheDocument()
    expect(screen.getByRole('button',{name:'Delete status'})).toBeDisabled()
    expect(deleteWorkflowState).not.toHaveBeenCalled()
    await chooseReplacement()
    fireEvent.click(screen.getByRole('button',{name:'Delete status'}))
    await waitFor(()=>expect(deleteWorkflowState).toHaveBeenCalledWith('team-1',staged.id,started.id))
  })

  it('keeps failures in the dialog with retry and displays deleting while the request runs',async()=>{
    setup();await openDelete();await chooseReplacement()
    vi.mocked(deleteWorkflowState).mockRejectedValueOnce(new Error('Status changed; retry'))
    fireEvent.click(screen.getByRole('button',{name:'Delete status'}))
    expect(await screen.findByRole('alert')).toHaveTextContent('Status changed; retry')
    let resolve!:()=>void
    vi.mocked(deleteWorkflowState).mockImplementationOnce(()=>new Promise(done=>{resolve=done}))
    fireEvent.click(screen.getByRole('button',{name:'Delete status'}))
    expect(screen.getByRole('button',{name:'Deleting…'})).toBeDisabled()
    expect(screen.getByRole('button',{name:'Cancel'})).toBeDisabled()
    resolve()
    await waitFor(()=>expect(screen.queryByRole('dialog',{name:'Delete issue status'})).not.toBeInTheDocument())
  })

  it('does not turn a failed group query into a zero count and can retry',async()=>{
    vi.mocked(listIssueRecordGroups).mockRejectedValueOnce(new Error('Count unavailable'))
    setup()
    await waitFor(()=>expect(screen.getByRole('alert')).toHaveTextContent('Count unavailable'))
    await openDelete()
    expect(screen.getByRole('button',{name:'Delete status'})).toBeDisabled()
    const dialog=screen.getByRole('dialog',{name:'Delete issue status'})
    fireEvent.click(within(dialog).getByRole('button',{name:'Try again'}))
    await screen.findByText('75,675 issues will be moved.')
    expect(deleteWorkflowState).not.toHaveBeenCalled()
  })

  it('requires a separate explicit default change before allowing deletion',async()=>{
    const state={...staged,default:true}
    const onDefaultChanged=vi.fn().mockResolvedValue(undefined)
    const props={teamId:'team-1',states:stateList,usage:0,loading:false,onRetry:vi.fn(),onClose:vi.fn(),onDeleted:vi.fn().mockResolvedValue(undefined),onDefaultChanged}
    const view=render(<I18nProvider><WorkflowStateDeleteDialog {...props} state={state}/></I18nProvider>)
    await chooseReplacement()
    expect(screen.getByRole('button',{name:'Delete status'})).toBeDisabled()
    fireEvent.click(screen.getByRole('button',{name:'Set replacement as default'}))
    await waitFor(()=>expect(updateWorkflowState).toHaveBeenCalledWith('team-1',started.id,{default:true}))
    expect(deleteWorkflowState).not.toHaveBeenCalled()
    view.rerender(<I18nProvider><WorkflowStateDeleteDialog {...props} state={{...state,default:false}}/></I18nProvider>)
    await waitFor(()=>expect(screen.getByRole('button',{name:'Delete status'})).toBeEnabled())
  })

  it('still confirms zero-usage deletion and requires choosing a different status',async()=>{
    vi.mocked(listIssueRecordGroups).mockResolvedValue({groups:[]})
    setup()
    await waitFor(()=>expect(screen.queryByText('Loading issue statuses…')).not.toBeInTheDocument())
    await openDelete()
    expect(screen.getByText('0 issues will be moved.')).toBeInTheDocument()
    expect(screen.getByRole('button',{name:'Delete status'})).toBeDisabled()
    await chooseReplacement()
    fireEvent.click(screen.getByRole('button',{name:'Delete status'}))
    await waitFor(()=>expect(deleteWorkflowState).toHaveBeenCalledWith('team-1',staged.id,started.id))
  })

  it('keeps the final status and reserved duplicate undeletable, with explicit layout classes',async()=>{
    setup();await screen.findByText(/75675/)
    const row=screen.getByText('Done').closest('.issue-state-row') as HTMLElement
    fireEvent.click(within(row).getByRole('button',{name:'Open menu'}))
    expect(await screen.findByRole('option',{name:'Delete'})).toBeDisabled()
    expect(row.querySelector('.issue-status-mark > svg')).toHaveAttribute('width','16')
    expect(row.querySelector('.ip-status-copy')).not.toBeNull()
    expect(row.querySelector('.ip-status-drag-handle')).toBeNull()
    const duplicates=screen.getAllByText('Duplicate')
    const duplicateRow=duplicates.map(item=>item.closest('.issue-state-row')).find(Boolean) as HTMLElement
    expect(within(duplicateRow).queryByRole('button',{name:'Open menu'})).not.toBeInTheDocument()
    expect(deleteWorkflowState).not.toHaveBeenCalled()
  })
})
