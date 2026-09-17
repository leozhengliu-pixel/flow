import type { ComponentProps } from 'react'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap } from '@/test/fixtures'
import { DetailPane } from './detail-pane'
vi.mock('@/components/issue/issue-description-editor',()=>({IssueDescriptionEditor:()=> <div/>}))
vi.mock('@/components/editor/composer',()=>({Composer:()=> <div/>}))

describe('issue detail feature gates',()=>{
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
})
