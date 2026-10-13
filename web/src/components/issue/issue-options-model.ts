import type { BootstrapData, Issue } from '@/types/flow'

/** A link the issue options menu's "Remove ▸" submenu can undo: the parent, then each relation. */
export interface IssueRemovableLink {
  key: string
  kind: 'parent' | 'blocks' | 'related' | 'blockedBy' | 'duplicate'
  /** "Parent issue", "Blocked issue" (this issue blocks it), "Related issue", "Blocking issue" (it blocks this one), "Duplicate of". */
  label: string
  /** Label of the action glyph (see issue-action-glyphs) drawn before the row. */
  glyph: string
  identifier: string
  title: string
  relationId: string
  /** Issue that owns the relation row, so the delete goes to the right record (inbound relations live on the other issue). */
  ownerId: string
}

const PRIORITY_NAMES = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
const ORDER: IssueRemovableLink['kind'][] = ['parent', 'blocks', 'related', 'blockedBy', 'duplicate']
const LINK: Record<Exclude<IssueRemovableLink['kind'], 'parent'>, { label: string; glyph: string }> = {
  blocks: { label: 'Blocked issue', glyph: 'Blocking…' },
  related: { label: 'Related issue', glyph: 'Related to…' },
  blockedBy: { label: 'Blocking issue', glyph: 'Blocked by…' },
  duplicate: { label: 'Duplicate of', glyph: 'Duplicate of…' },
}

/** The reference "Remove ▸" rows: the parent first (in its own group), then relations by type. Sub-issues are not listed. */
export function issueRemovableLinks(issue: Issue, data: BootstrapData): IssueRemovableLink[] {
  const byId = new Map(data.issues.map(item => [item.id, item]))
  const links: IssueRemovableLink[] = []
  const parent = issue.parentId ? byId.get(issue.parentId) : undefined
  if (issue.parentId) links.push({ key: `parent:${issue.parentId}`, kind: 'parent', label: 'Parent issue', glyph: 'Sub-issue of…', identifier: parent?.identifier ?? '', title: parent?.title ?? '', relationId: '', ownerId: issue.id })
  const seen = new Set<string>()
  const add = (relationId: string, ownerId: string, kind: Exclude<IssueRemovableLink['kind'], 'parent'>, otherId: string) => {
    if (seen.has(relationId) || seen.has(`${kind}:${otherId}`)) return
    seen.add(relationId); seen.add(`${kind}:${otherId}`)
    const other = byId.get(otherId)
    if (!other) return
    links.push({ key: relationId, kind, ...LINK[kind], identifier: other.identifier, title: other.title, relationId, ownerId })
  }
  for (const relation of issue.relations) {
    const kind = relation.type === 'blocks' ? 'blocks' : relation.type === 'blocked_by' ? 'blockedBy' : relation.type === 'related' ? 'related' : relation.type === 'duplicate' ? 'duplicate' : undefined
    if (kind) add(relation.id, issue.id, kind, relation.relatedIssueId)
  }
  // Relations recorded only on the other issue, seen from this side.
  for (const source of data.issues) {
    if (source.id === issue.id) continue
    for (const relation of source.relations) {
      if (relation.relatedIssueId !== issue.id) continue
      const kind = relation.type === 'blocks' ? 'blockedBy' : relation.type === 'blocked_by' ? 'blocks' : relation.type === 'related' ? 'related' : relation.type === 'duplicate' ? 'duplicate' : undefined
      if (kind) add(relation.id, source.id, kind, source.id)
    }
  }
  return links.sort((left, right) => ORDER.indexOf(left.kind) - ORDER.indexOf(right.kind))
}

/** "Copy issue as Markdown": the title as a heading, then the description. */
export function issueMarkdown(issue: Pick<Issue, 'title' | 'description'>) {
  return `# ${issue.title}\n\n${issue.description ? `${issue.description}\n\n` : ''}`
}

/** "Copy everything as Markdown": the issue followed by the reference app's "## Metadata" list. */
export function issueEverythingMarkdown(issue: Issue, url: string, data?: BootstrapData) {
  const labels = issue.labels.map(label => label.name).join(', ')
  const lines = [
    `- URL: [${url}](${url})`,
    `- Identifier: ${issue.identifier}`,
    `- Status: ${issue.state.name}`,
    `- Priority: ${PRIORITY_NAMES[issue.priority] ?? 'No priority'}`,
    `- Assignee: ${issue.assignee?.displayName ?? 'Unassigned'}`,
    ...(labels ? [`- Labels: ${labels}`] : []),
    ...(issue.project ? [`- Project: ${data?.projects.find(project => project.id === issue.project?.id)?.name ?? issue.project.name}`] : []),
    ...(issue.dueDate ? [`- Due date: ${issue.dueDate.slice(0, 10)}`] : []),
    `- Created: ${issue.createdAt}`,
    `- Updated: ${issue.updatedAt}`,
  ]
  return `${issueMarkdown(issue)}## Metadata\n${lines.join('\n')}`
}
