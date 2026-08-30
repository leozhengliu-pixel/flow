import { useMemo, useState } from 'react'

import { MyIssuesList, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { explorerPropertyOptions, explorerUpdateForProperty, issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { UserAvatar } from '@/components/ui/user-avatar'
import type { BootstrapData, Issue, IssueUpdateInput, User } from '@/types/flow'

import './workspace-directory.css'

export function MemberProfilePage({data,user,view,onNavigate,onOpenIssue,onUpdateIssue}:{data:BootstrapData;user:User;view:'assigned'|'created';onNavigate:(view:'assigned'|'created')=>void;onOpenIssue:(issue:Issue)=>void;onUpdateIssue:(id:string,input:IssueUpdateInput)=>Promise<Issue>}){
  const [selected,setSelected]=useState<Set<string>>(new Set())
  const issues=useMemo(()=>data.issues.filter(issue=>view==='assigned'?issue.assignee?.id===user.id:issue.creator.id===user.id),[data.issues,user.id,view])
  const rows=useMemo(()=>issues.map(issue=>issueToExplorerRow(issue,data.workspace.urlKey,data.issues,data)),[data,issues])
  const groups=useMemo(()=>groupByStatus(rows),[rows])
  const options=useMemo(()=>explorerPropertyOptions(data,issues),[data,issues])
  const change=async(row:MyIssuesRowData,property:MyIssuesEditableProperty,value:string|string[])=>{const input=explorerUpdateForProperty(property,value);if(input)await onUpdateIssue(row.id,input)}
  return <main className="main-panel member-profile-page">
    <header className="member-profile-header"><UserAvatar avatarUrl={user.avatarUrl} color="#5e6ad2" name={user.displayName}/><h1 data-i18n-ignore>{user.displayName}</h1></header>
    <nav className="member-profile-tabs"><button aria-current={view==='assigned'} onClick={()=>onNavigate('assigned')}>Assigned</button><button aria-current={view==='created'} onClick={()=>onNavigate('created')}>Created</button></nav>
    <section className="member-profile-list"><MyIssuesList groups={groups} propertyOptions={options} selectedIds={selected} onOpenIssue={row=>{const issue=data.issues.find(item=>item.id===row.id);if(issue)onOpenIssue(issue)}} onPropertyChange={change} onSelectIssue={(id,checked)=>setSelected(current=>{const next=new Set(current);if(checked)next.add(id);else next.delete(id);return next})}/></section>
  </main>
}

function groupByStatus(rows:MyIssuesRowData[]):MyIssuesGroupData[]{const groups=new Map<string,MyIssuesGroupData>();for(const row of rows){const group=groups.get(row.state.id)??{id:row.state.id,label:row.state.name,stateType:row.state.type,state:row.state,issues:[]};group.issues.push(row);groups.set(row.state.id,group)}return [...groups.values()]}
