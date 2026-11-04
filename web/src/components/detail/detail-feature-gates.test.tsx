import type { ComponentProps } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import type { User } from '@/types/flow'
import { makeBootstrap } from '@/test/fixtures'
import { DetailPane } from './detail-pane'
vi.mock('@/components/issue/issue-description-editor',()=>({IssueDescriptionEditor:({readOnly}:{readOnly?:boolean})=> <div data-testid="description" data-readonly={Boolean(readOnly)}/>}))
vi.mock('@/components/editor/composer',()=>({Composer:()=> <div/>}))
const layout=vi.hoisted(()=>({desktop:false}))
vi.mock('@/hooks/use-desktop-layout',()=>({useDesktopLayout:()=>layout.desktop}))
vi.mock('./similar-issues',()=>({SimilarIssues:()=> <section aria-label="Similar issues"/>}))

describe('issue detail feature gates',()=>{
  beforeEach(()=>{layout.desktop=false})
  it('keeps archived title, description and properties read-only until restoration',async()=>{
    const data=makeBootstrap({cycleSettings:{},teamSettings:{},documents:[],members:[],drafts:[],reviews:[],favorites:[],customers:[],teamMembers:[],userSettings:{},projectTemplates:[],agentSkills:[],customerRequests:[],issueSlas:[],slaRules:[]})
    data.workspaceSettings={...data.workspaceSettings,featureFlags:{releases:false,'customer-requests':false,'triage-intelligence':false}}
    const props={issue:{...data.issues[0],archivedAt:'2026-11-03T00:00:00Z'},data,comments:[],activities:[],onClose:vi.fn(),onUpdate:vi.fn(),onDelete:vi.fn(),onCreateSubIssue:vi.fn(),onReactIssue:vi.fn(),onComment:vi.fn(),onEditComment:vi.fn(),onDeleteComment:vi.fn(),onReactComment:vi.fn(),onRelation:vi.fn(),onDeleteRelation:vi.fn(),onUpload:vi.fn(),onDeleteAttachment:vi.fn()} as ComponentProps<typeof DetailPane>
    const view=(issue=props.issue)=><MemoryRouter><I18nProvider><TooltipProvider><DetailPane {...props} issue={issue}/></TooltipProvider></I18nProvider></MemoryRouter>
    const {rerender}=render(view())
    await waitFor(()=>expect(screen.getByLabelText('Issue title')).toHaveAttribute('contenteditable','false'))
    expect(screen.getByTestId('description')).toHaveAttribute('data-readonly','true')
    for(const trigger of screen.getAllByRole('combobox',{name:/^Change priority\./}))expect(trigger).toBeDisabled()
    rerender(view({...props.issue,archivedAt:undefined}))
    await waitFor(()=>expect(screen.getByLabelText('Issue title')).toHaveAttribute('contenteditable','true'))
    expect(screen.getByTestId('description')).toHaveAttribute('data-readonly','false')
    for(const trigger of screen.getAllByRole('combobox',{name:/^Change priority\./}))expect(trigger).not.toBeDisabled()
    expect(props.onUpdate).not.toHaveBeenCalled()
  })
  it.each([false,true])('keeps automatic similarity suggestions off desktop without changing phones: %s',desktop=>{
    layout.desktop=desktop
    const data=makeBootstrap({cycleSettings:{},teamSettings:{},documents:[],members:[],drafts:[],reviews:[],favorites:[],customers:[],teamMembers:[],userSettings:{},projectTemplates:[],agentSkills:[],customerRequests:[],issueSlas:[],slaRules:[]})
    data.workspaceSettings={...data.workspaceSettings,featureFlags:{releases:false,'customer-requests':false,'triage-intelligence':false}}
    const props={issue:data.issues[0],data,comments:[],activities:[],onClose:vi.fn(),onUpdate:vi.fn(),onDelete:vi.fn(),onCreateSubIssue:vi.fn(),onReactIssue:vi.fn(),onComment:vi.fn(),onEditComment:vi.fn(),onDeleteComment:vi.fn(),onReactComment:vi.fn(),onRelation:vi.fn(),onDeleteRelation:vi.fn(),onUpload:vi.fn(),onDeleteAttachment:vi.fn()} as ComponentProps<typeof DetailPane>
    render(<MemoryRouter><I18nProvider><TooltipProvider><DetailPane {...props}/></TooltipProvider></I18nProvider></MemoryRouter>)
    expect(screen.queryByRole('region',{name:'Similar issues'})!==null).toBe(!desktop)
  })
  it.each([undefined,false,true])('renders cycle controls only for explicitly enabled teams: %s',enabled=>{
    const data=makeBootstrap({cycleSettings:{},teamSettings:{},documents:[],members:[],drafts:[],reviews:[],favorites:[],customers:[],teamMembers:[],userSettings:{},projectTemplates:[],agentSkills:[],customerRequests:[],issueSlas:[],slaRules:[]})
    data.workspaceSettings={...data.workspaceSettings,featureFlags:{releases:false,'customer-requests':false,'triage-intelligence':false}}
    const issue=data.issues[0]
    if(enabled!==undefined)data.cycleSettings[issue.team.id]={enabled} as typeof data.cycleSettings[string]
    const props={issue,data,comments:[],activities:[],onClose:vi.fn(),onUpdate:vi.fn(),onDelete:vi.fn(),onCreateSubIssue:vi.fn(),onReactIssue:vi.fn(),onComment:vi.fn(),onEditComment:vi.fn(),onDeleteComment:vi.fn(),onReactComment:vi.fn(),onRelation:vi.fn(),onDeleteRelation:vi.fn(),onUpload:vi.fn(),onDeleteAttachment:vi.fn()} as ComponentProps<typeof DetailPane>
    render(<MemoryRouter><I18nProvider><TooltipProvider><DetailPane {...props}/></TooltipProvider></I18nProvider></MemoryRouter>)
    expect(screen.queryAllByRole('combobox',{name:'Add to cycle'}).length>0).toBe(enabled===true)
    expect(screen.queryByRole('button',{name:'Set releases'})).toBeNull()
  })
  it('has no separate "Delegate to agent" control: agents live in the Assignee picker, which shows the delegate',()=>{
    const agent={id:'app_flow',name:'Flow',displayName:'Flow',email:'',active:true,app:true,builtinAgent:true,appScopes:['read','app:assignable'],avatarUrl:'/favicon.svg'} as User
    const data=makeBootstrap({cycleSettings:{},teamSettings:{},documents:[],members:[],drafts:[],reviews:[],favorites:[],customers:[],teamMembers:[],userSettings:{},projectTemplates:[],agentSkills:[],customerRequests:[],issueSlas:[],slaRules:[]})
    data.workspaceSettings={...data.workspaceSettings,featureFlags:{releases:false,'customer-requests':false,'triage-intelligence':false}}
    const issue=data.issues[0]
    agent.appTeamIds=[issue.team.id]
    data.users=[...data.users,agent]
    issue.delegate=agent
    const props={issue,data,comments:[],activities:[],onClose:vi.fn(),onUpdate:vi.fn(),onDelete:vi.fn(),onCreateSubIssue:vi.fn(),onReactIssue:vi.fn(),onComment:vi.fn(),onEditComment:vi.fn(),onDeleteComment:vi.fn(),onReactComment:vi.fn(),onRelation:vi.fn(),onDeleteRelation:vi.fn(),onUpload:vi.fn(),onDeleteAttachment:vi.fn()} as ComponentProps<typeof DetailPane>
    render(<MemoryRouter><I18nProvider><TooltipProvider><DetailPane {...props}/></TooltipProvider></I18nProvider></MemoryRouter>)
    expect(screen.queryByRole('combobox',{name:'Delegate to agent'})).toBeNull()
    expect(screen.queryByText('Delegate to agent')).toBeNull()
    const assignee=screen.getAllByRole('combobox',{name:/^Change assignee/})[0]
    expect(assignee).toHaveTextContent('Flow')
  })
})
