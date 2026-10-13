import { issueEverythingMarkdown, issueMarkdown } from '@/components/issue/issue-options-model'
import { initiativePath, issuePath, projectPath } from '@/lib/app-routes'
import { configuredIssueBranch } from '@/lib/issue-work-actions'
import type { BootstrapData, Initiative, Issue, Project, TrashEntry } from '@/types/flow'

/** The reference app's restore item per recently deleted list ("Restore" on issues, "Restore project", …). */
export function trashRestoreLabel(resourceType: string) {
  switch (resourceType) {
    case 'project': return 'Restore project'
    case 'initiative': return 'Restore initiative'
    case 'document': return 'Restore document'
    default: return 'Restore'
  }
}

export type TrashCopyItem = { label: string; glyph: string; shortcut?: string; value: () => string }

/** The record a trash entry keeps (issues, projects and initiatives are wrapped with their content). */
function trashedRecord<T>(entry: TrashEntry, key: string): Partial<T> {
  const payload = entry.payload && typeof entry.payload === 'object' ? entry.payload as Record<string, unknown> : {}
  const record = payload[key]
  return (record && typeof record === 'object' ? record : payload) as Partial<T>
}

/** The reference app's Copy ▸ items for a recently deleted issue, project or initiative (documents use their own menu). */
export function trashCopyItems(entry: TrashEntry, data: BootstrapData): TrashCopyItem[] {
  const origin = typeof window === 'undefined' ? '' : window.location.origin
  const workspace = data.workspace.urlKey
  if (entry.resourceType === 'issue') {
    const issue = trashedRecord<Issue>(entry, 'issue')
    const identifier = issue.identifier ?? entry.title.split(' ')[0]
    const title = issue.title ?? entry.title
    const url = origin + issuePath(workspace, { identifier, title })
    return [
      { label: 'Copy ID', glyph: 'Copy ID', shortcut: '⌘ .', value: () => identifier },
      { label: 'Copy URL', glyph: 'Copy URL', shortcut: '⌘ ⇧ ,', value: () => url },
      { label: 'Copy title', glyph: 'Copy title', shortcut: "⌘ ⇧ '", value: () => title },
      { label: 'Copy title as link', glyph: 'Copy title as link', shortcut: '⌘ C', value: () => `[${identifier} ${title}](${url})` },
      { label: 'Copy issue as Markdown', glyph: 'Copy issue as Markdown', value: () => issueMarkdown({ title, description: issue.description ?? '' }) },
      { label: 'Copy everything as Markdown', glyph: 'Copy everything as Markdown', shortcut: '⌘ ⌥ C', value: () => issue.state && issue.labels ? issueEverythingMarkdown(issue as Issue, url, data) : issueMarkdown({ title, description: issue.description ?? '' }) },
      { label: 'Copy git branch name', glyph: 'Copy git branch name', shortcut: '⌘ ⇧ .', value: () => issue.team ? configuredIssueBranch(issue as Issue, data) : identifier.toLowerCase() },
    ]
  }
  const record = entry.resourceType === 'project' ? trashedRecord<Project>(entry, 'project') : trashedRecord<Initiative>(entry, 'initiative')
  const name = record.name ?? entry.title
  const slugId = record.slugId ?? entry.resourceId
  const url = origin + (entry.resourceType === 'project' ? projectPath(workspace, { slugId }) : initiativePath(workspace, { slugId }))
  const overview = (record as Partial<Project>).summary ?? (record as Partial<Initiative>).description ?? ''
  return [
    { label: 'Copy ID', glyph: 'Copy ID', shortcut: '⌘ .', value: () => slugId },
    { label: 'Copy URL', glyph: 'Copy URL', shortcut: '⌘ ⇧ ,', value: () => url },
    { label: 'Copy title', glyph: 'Copy title', shortcut: "⌘ ⇧ '", value: () => name },
    { label: 'Copy title as link', glyph: 'Copy title as link', shortcut: '⌘ C', value: () => `[${name}](${url})` },
    { label: 'Copy overview as Markdown', glyph: 'Copy content as Markdown', shortcut: '⌘ ⌥ C', value: () => `# ${name}\n\n${overview}`.trim() },
  ]
}
