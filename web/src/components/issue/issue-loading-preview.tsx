import { ArrowLeft } from 'lucide-react'
import type { Issue } from '@/types/flow'
import { AgentRichText } from '@/components/agent/agent-rich-text'
import { StatusIcon, TeamIcon } from './issue-icons'
import './issue-loading-preview.css'

// Full, authorized content may arrive before the workspace directories. Keep
// this surface read-only until the actual detail editor can be initialized.
export function IssueLoadingPreview({issue,onBack}:{issue:Issue;onBack:()=>void}) {
  if (issue.isSummary) return <div role="status">Loading issue…</div>
  return <section className="issue-loading-preview" aria-label="Issue preview" aria-busy="true">
    <header><button type="button" onClick={onBack} aria-label="Back"><ArrowLeft size={16}/></button><TeamIcon team={issue.team} size={16}/><span>{issue.identifier}</span></header>
    <div className="issue-loading-preview-content"><h1>{issue.title}</h1><div className="issue-loading-preview-properties"><StatusIcon state={issue.state} size={16}/><span>{issue.state.name}</span>{issue.assignee&&<span>{issue.assignee.displayName}</span>}{issue.project&&<span>{issue.project.name}</span>}</div><AgentRichText className="issue-loading-preview-description" ariaLabel="Issue description" content={issue.description}/></div>
  </section>
}
