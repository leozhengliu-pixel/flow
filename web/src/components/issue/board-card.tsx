import { MessageSquare } from 'lucide-react'
import type { Issue } from '@/types/flow'
import { Avatar } from './issue-row'
import { PriorityIcon } from './issue-icons'
export function BoardCard({issue,onOpen}:{issue:Issue;onOpen:(issue:Issue)=>void}){return <button className="board-card" onClick={()=>onOpen(issue)}><div className="card-id"><PriorityIcon priority={issue.priority}/>{issue.identifier}</div><strong>{issue.title}</strong><div className="card-footer"><span className="card-labels">{issue.labels.map(l=><i key={l.id} title={l.name} style={{background:l.color}}/>)}</span><span><MessageSquare size={13}/> {issue.subIssueIds.length}</span>{issue.assignee&&<Avatar name={issue.assignee.displayName}/>}</div></button>}
