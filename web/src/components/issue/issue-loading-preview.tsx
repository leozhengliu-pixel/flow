import { ArrowLeft } from 'lucide-react'
import type { Issue } from '@/types/flow'
import { AgentRichText } from '@/components/agent/agent-rich-text'
import { StatusIcon, TeamIcon } from './issue-icons'
import './issue-loading-preview.css'

// Full, authorized content may arrive before the workspace directories. Keep
// this surface read-only until the actual detail editor can be initialized.
export function IssueLoadingPreview({issue,onBack}:{issue:Issue;onBack:()=>void}) {
  if (issue.isSummary) return <div role="status">Loading issue…</div>
  const properties = <><StatusIcon state={issue.state} size={16}/><span>{issue.state.name}</span>{issue.assignee&&<span>{issue.assignee.displayName}</span>}{issue.project&&<span>{issue.project.name}</span>}</>
  return <section className="issue-loading-preview issue-view" aria-label="Issue preview" aria-busy="true">
    <header className="issue-header issue-loading-preview-header"><div className="issue-header-context"><button className="issue-header-icon" type="button" onClick={onBack} aria-label="Back"><ArrowLeft size={16}/></button><TeamIcon team={issue.team} size={16}/><strong>{issue.identifier}</strong></div></header>
    <div className="issue-scroll"><div className="issue-layout">
      <article className="issue-document issue-loading-preview-document">
        <div className="issue-title-field"><div aria-level={1} className="flow-prosemirror title-editor issue-loading-preview-title" role="heading"><p>{issue.title}</p></div></div>
        <div className="issue-mobile-properties issue-loading-preview-mobile-properties">{properties}</div>
        <div className="issue-description-root issue-loading-preview-description-root"><AgentRichText className="flow-prosemirror description-editor issue-loading-preview-description" ariaLabel="Issue description" content={issue.description}/></div>
      </article>
      <aside className="issue-properties issue-loading-preview-properties"><h3>Properties</h3><div className="issue-loading-preview-property">{properties}</div></aside>
    </div></div>
  </section>
}
