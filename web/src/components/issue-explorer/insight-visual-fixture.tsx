import { useState } from 'react'
import { MyIssuesPage } from '@/components/my-issues/my-issues-page'
import { makeBootstrap, makeIssue, backlog, started, completed, viewer, teammate } from '@/test/fixtures'
import type { Issue, IssueUpdateInput, WorkflowState } from '@/types/flow'

const params = new URLSearchParams(location.search)
const states: WorkflowState[] = [backlog, { ...backlog, id: 'todo', name: 'Todo', type: 'unstarted', position: 1 }, { ...started, position: 2 }, { ...started, id: 'review', name: 'In Review', position: 3 }, { ...completed, position: 4 }]
const count = Math.min(10000, Math.max(0, Number(params.get('count') ?? 13) || 0))
const fixtures = Array.from({ length: count }, (_, i) => makeIssue({ id: `fixture-${i}`, identifier: `TEST-${i+1}`, title: `Analysis test ${i+1}`, state: states[count === 13 ? i < 1 ? 0 : i < 3 ? 1 : i < 6 ? 2 : i < 8 ? 3 : 4 : i % 5], creator: params.has('unique') ? { ...viewer, id: `creator-${i}`, displayName: `Creator ${i}` } : i % 2 ? viewer : teammate, assignee: viewer, startedAt: '2026-08-01T00:00:00Z', completedAt: i > 7 ? `2026-08-${String(2 + i % 20).padStart(2, '0')}T00:00:00Z` : undefined }))
export function Fixture() {
  const [issues, setIssues] = useState(fixtures)
  const data = makeBootstrap({ issues, states, issueSlas: [], slaRules: [], teamSettings: {} })
  const update = async (id: string, input: IssueUpdateInput) => { const issue = { ...issues.find(issue => issue.id === id)!, ...input } as Issue; setIssues(current => current.map(item => item.id === id ? issue : item)); return issue }
  return <div className="insight-fixture"><MyIssuesPage data={data} onOpenIssue={issue => { document.title = issue.identifier }} onUpdateIssue={update} onUpdateIssues={(ids, input) => Promise.all(ids.map(id => update(id, input)))} onDeleteIssues={async ids => setIssues(current => current.filter(issue => !ids.includes(issue.id)))}/></div>
}
