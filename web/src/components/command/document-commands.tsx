/**
 * Document commands for ⌘K: the global ones (open, create in…, templates, team
 * overview) and, with a document registered as the command context, the
 * document-scoped ones (owner, favorite, copy, remind, move, pin, templates,
 * subscribers, rename, delete). Every write goes through the shared
 * document-actions so wording, toasts and behaviour match the menus.
 */
import { parseDate } from 'chrono-node'
import {
  AlarmClock, BellOff, Bell, Building2, CopyPlus, FilePlus2, FileText, FolderInput, History, LayoutTemplate, Lightbulb, Link2, Pencil, Pin,
  Star, Trash2, Type, User, UserPlus, Users,
} from 'lucide-react'

import { DocumentGlyph } from '@/components/documents/document-icon'
import { toast } from 'sonner'

import {
  applyTemplateToDocument, changeDocumentOwner, copyDocumentMarkdown, copyDocumentTitle, copyDocumentTitleAsLink, copyDocumentUrl,
  createDocumentIn, createTemplateFromDocument, deleteDocumentsWithConfirm, documentAbsoluteUrl, documentDisplayTitle, documentOwner,
  documentParent, documentPinnedResource, duplicateDocument, moveDocument, remindAboutDocument, remindAboutDocumentCustom, renameDocument,
  requestDocumentUiAction, setDocumentSubscribers, toggleDocumentFavorite, toggleDocumentPinnedToTeam, viewerTeams,
  type DocumentActionContext, type DocumentMoveTarget,
} from '@/components/documents/document-actions'
import { documentOwnerOptions } from '@/components/documents/document-owner-options'
import { NoAssigneeIcon, ProjectIcon, TeamIcon } from '@/components/issue/issue-icons'
import { reminderPresetOptions } from '@/components/ui/reminder-presets'
import { UserAvatar } from '@/components/ui/user-avatar'
import { documentPath, initiativePath, projectPath, settingsPath, teamArchivePath, teamDocumentsPath, workspaceRootPath } from '@/lib/app-routes'
import type { BootstrapData, DocumentTemplate, FlowDocument, Team } from '@/types/flow'
import type { CommandContext } from './command-context'
import type { CommandPage, ContextAction, PageOption } from './context-commands'

/** What the palette needs from the app to run document writes. */
export interface DocumentCommandHost {
  reload: () => Promise<void>
  navigate: (path: string) => void
}

type Translate = (value: string) => string

export interface DocumentCommandResult {
  /** Always-available document commands (listed in the "Documents" group). */
  global: ContextAction[]
  /** Commands for the document in the command context (empty otherwise). */
  actions: ContextAction[]
  /** Options of the open document page. */
  options: PageOption[]
}

const EMPTY: DocumentCommandResult = { global: [], actions: [], options: [] }
const page = (id: CommandPage['id'], label: string, payload?: string): CommandPage => ({ id, label, ...(payload ? { payload } : {}) })

function parentOptions(data: BootstrapData, current: Set<string>, choose: (target: DocumentMoveTarget) => () => void | Promise<unknown>): PageOption[] {
  return [
    ...viewerTeams(data).map(team => ({ id: `team:${team.id}`, label: team.name, entity: true, group: 'My teams', keywords: team.key, icon: <TeamIcon team={team} size={16}/>, current: current.has(`team:${team.id}`), select: choose({ type: 'team', id: team.id }) })),
    ...data.projects.filter(project => !project.archivedAt).map(project => ({ id: `project:${project.id}`, label: project.name, entity: true, group: 'Projects', icon: <ProjectIcon style={{ color: project.color }}/>, current: current.has(`project:${project.id}`), select: choose({ type: 'project', id: project.id }) })),
    ...data.initiatives.map(initiative => ({ id: `initiative:${initiative.id}`, label: initiative.name, entity: true, group: 'Initiatives', icon: <Lightbulb/>, current: current.has(`initiative:${initiative.id}`), select: choose({ type: 'initiative', id: initiative.id }) })),
  ]
}

/** Workspace templates first, then each of the viewer's teams' (grouped under the team name). */
function templateOptions(data: BootstrapData, t: Translate, choose: (template: DocumentTemplate) => Partial<PageOption>): PageOption[] {
  const teamName = (template: DocumentTemplate) => data.teams.find(team => team.id === template.teamId)?.name ?? ''
  const visible = (data.documentTemplates ?? []).filter(template => !template.teamId || data.teams.some(team => team.id === template.teamId && !team.archivedAt))
  const ordered = [...visible.filter(template => !template.teamId), ...visible.filter(template => template.teamId).sort((a, b) => teamName(a).localeCompare(teamName(b)))]
  return ordered.map(template => ({
    id: template.id, label: template.name, entity: true, group: template.teamId ? teamName(template) : t('Workspace'), keywords: `${template.title ?? ''} ${teamName(template)}`,
    icon: template.teamId ? <TeamIcon team={data.teams.find(team => team.id === template.teamId)} size={16}/> : <Building2/>, ...choose(template),
  }))
}

function parentLabel(data: BootstrapData, document: FlowDocument) {
  const parent = documentParent(data, document)
  if (!parent) return undefined
  if (parent.type === 'team') return parent.team.name
  if (parent.type === 'project') return parent.project.name
  if (parent.type === 'initiative') return parent.initiative.name
  return undefined
}

/** Where to land after the open document is deleted: its parent's page. */
function documentParentPath(data: BootstrapData, document: FlowDocument) {
  const workspace = data.workspace.urlKey
  const parent = documentParent(data, document)
  if (parent?.type === 'project') return projectPath(workspace, parent.project)
  if (parent?.type === 'initiative') return initiativePath(workspace, parent.initiative)
  const team = parent?.type === 'team' ? parent.team : viewerTeams(data)[0]
  return team ? teamDocumentsPath(workspace, team.key) : workspaceRootPath(workspace)
}

export function documentCommands({ context, data: source, page: current, query, host, close, t, pathname }: {
  context: CommandContext | undefined
  data: BootstrapData | undefined
  page: CommandPage | undefined
  query: string
  host: DocumentCommandHost
  close: () => void
  t: Translate
  /** The current URL path, to leave a deleted document's page. */
  pathname: string
}): DocumentCommandResult {
  if (!source) return EMPTY
  const data: BootstrapData = { ...source, documents: source.documents ?? [], favorites: source.favorites ?? [], teamMembers: source.teamMembers ?? [], documentTemplates: source.documentTemplates ?? [], initiatives: source.initiatives ?? [], teamPinnedResources: source.teamPinnedResources ?? [] }
  const ctx: DocumentActionContext = { data, reload: host.reload, navigate: host.navigate, t }
  const workspace = data.workspace.urlKey
  const open = (document: Pick<FlowDocument, 'slugId'>) => host.navigate(documentPath(workspace, document))
  const choose = (work: () => unknown): (() => void | Promise<unknown>) => () => { close(); return work() as void | Promise<unknown> }
  const teams = viewerTeams(data)
  const templates = data.documentTemplates ?? []

  const global: ContextAction[] = [
    { id: 'open-document', label: 'Open document…', icon: <FileText/>, shortcut: ['O', 'then', 'D'], keywords: 'goto find', page: page('documentOpen', 'Open document…') },
    { id: 'go-deleted-documents', label: 'Go to recently deleted documents', icon: <Trash2/>, keywords: 'trash restore', run: () => {
      close()
      const team = teams[0] ?? data.teams[0]
      if (team) host.navigate(teamArchivePath(workspace, team.key, 'recently-deleted-documents'))
    } },
    { id: 'create-document', label: 'Create new document in…', icon: <FilePlus2/>, keywords: 'new', page: page('documentCreateIn', 'Create new document in…') },
    { id: 'create-document-template', label: 'Create new document template…', icon: <LayoutTemplate/>, run: () => { close(); host.navigate(`${settingsPath(workspace, 'documents')}?newTemplate=1`) } },
    ...(templates.length ? [{ id: 'create-document-from-template', label: 'Create new document from template…', icon: <LayoutTemplate/>, keywords: 'new', page: page('documentFromTemplate', 'Create new document from template…') }] : []),
    ...(teams.length ? [{ id: 'add-document-to-overview', label: 'Add document to team overview…', icon: <Pin/>, keywords: 'pin resources', page: page('documentTeamOverview', 'Add document to team overview…') }] : []),
  ]

  // Fresh records (favorites and subscribers change while the palette is open); a list selection can hold several.
  const docs = context?.kind === 'document' ? (context.documents ?? [context.document]).map(item => data.documents.find(found => found.id === item.id) ?? item) : []
  const doc = docs[0]
  const actions: ContextAction[] = doc ? documentActions(ctx, docs, { host, close, pathname }) : []
  const options = pageOptions({ ctx, data, docs, page: current, query, t, open, choose })
  return { global, actions, options }
}

function documentActions(ctx: DocumentActionContext, documents: FlowDocument[], { host, close, pathname }: { host: DocumentCommandHost; close: () => void; pathname: string }): ContextAction[] {
  const { data, t } = ctx
  const document = documents[0]
  const many = documents.length > 1
  const viewerId = data.viewer.id
  const isFavorite = (item: FlowDocument) => data.favorites.some(favorite => favorite.userId === viewerId && favorite.resourceType === 'document' && favorite.resourceId === item.id) || item.favorite
  const favorited = documents.every(isFavorite)
  const subscribed = documents.every(item => item.subscriberIds.includes(viewerId))
  const here = pathname === documentPath(data.workspace.urlKey, document)
  const each = (work: (item: FlowDocument) => unknown) => (): void | Promise<unknown> => { close(); return (async () => { for (const item of documents) await work(item) })() }
  const copyAll = (text: (item: FlowDocument) => string, notice: string) => (): void | Promise<unknown> => { close(); return copyText(documents.map(text).join('\n'), t(notice), t('Could not copy to clipboard')) }
  /** Page-level surfaces (history dialog, subscriber popover, author names) when that page is open, else the page itself. */
  const surface = (action: 'history' | 'subscribers' | 'authors') => () => {
    close()
    if (requestDocumentUiAction(action, document.id)) return
    host.navigate(`${documentPath(data.workspace.urlKey, document)}${action === 'history' ? '?history' : ''}`)
  }
  const single = (action: ContextAction): ContextAction[] => many ? [] : [action]
  return [
    { id: 'doc-owner', label: 'Change owner', icon: <User/>, shortcut: ['Ctrl', '⌘', 'O'], keywords: 'assign creator', page: page('documentOwner', 'Change owner') },
    { id: 'doc-favorite', label: favorited ? 'Unfavorite document' : 'Favorite document', icon: <Star fill={favorited ? 'currentColor' : 'none'}/>, shortcut: ['⌥', 'F'], keywords: 'star', run: each(item => isFavorite(item) === favorited ? toggleDocumentFavorite(ctx, item) : undefined) },
    { id: 'doc-copy-url', label: 'Copy document URL', icon: <Link2/>, shortcut: ['⌘', '⇧', ','], keywords: 'link', run: many ? copyAll(item => documentAbsoluteUrl(data.workspace.urlKey, item), 'Copied document link to clipboard') : each(item => copyDocumentUrl(ctx, item)) },
    { id: 'doc-copy-title', label: 'Copy document title', icon: <Type/>, shortcut: ['⌘', '⇧', "'"], run: many ? copyAll(item => documentDisplayTitle(item, t), 'Copied document title to clipboard') : each(item => copyDocumentTitle(ctx, item)) },
    ...single({ id: 'doc-copy-title-link', label: 'Copy title as link', icon: <Link2/>, shortcut: ['⌘', 'C'], keywords: 'markdown', run: each(item => copyDocumentTitleAsLink(ctx, item)) }),
    ...single({ id: 'doc-copy-markdown', label: 'Copy document content as Markdown', icon: <FileText/>, shortcut: ['⌘', '⌥', 'C'], run: each(item => copyDocumentMarkdown(ctx, item)) }),
    { id: 'doc-remind', label: 'Remind me about this document…', icon: <AlarmClock/>, shortcut: ['⇧', 'H'], keywords: 'reminder snooze', page: page('documentRemind', 'Remind me about this document…') },
    ...single({ id: 'doc-duplicate', label: 'Duplicate as new document', icon: <CopyPlus/>, keywords: 'copy clone', run: () => { close(); void duplicateDocument(ctx, document).then(created => { if (created) host.navigate(documentPath(data.workspace.urlKey, created)) }) } }),
    ...single({ id: 'doc-history', label: 'Document history', icon: <History/>, keywords: 'versions revisions restore', run: surface('history') }),
    ...single({ id: 'doc-subscribers', label: 'Change document subscribers…', icon: <Users/>, shortcut: ['⌘', '⇧', 'S'], keywords: 'notifications', run: () => { close(); if (!requestDocumentUiAction('subscribers', document.id)) host.navigate(documentPath(data.workspace.urlKey, document)) } }),
    subscribed
      ? { id: 'doc-subscribe', label: 'Unsubscribe from document updates', icon: <BellOff/>, shortcut: ['⇧', 'S'], keywords: 'notifications', run: each(item => setDocumentSubscribers(ctx, item, item.subscriberIds.filter(id => id !== viewerId))) }
      : { id: 'doc-subscribe', label: 'Subscribe to document updates', icon: <Bell/>, shortcut: ['⇧', 'S'], keywords: 'notifications', run: each(item => item.subscriberIds.includes(viewerId) ? undefined : setDocumentSubscribers(ctx, item, [...item.subscriberIds, viewerId])) },
    ...single({ id: 'doc-authors', label: 'Show author names', icon: <UserPlus/>, shortcut: ['⇧', 'A'], keywords: 'authors hide', run: surface('authors') }),
    ...single({ id: 'doc-rename', label: 'Rename document', icon: <Pencil/>, shortcut: ['⇧', 'R'], run: each(item => renameDocument(ctx, item)) }),
    { id: 'doc-move', label: 'Move to…', icon: <FolderInput/>, shortcut: ['⇧', 'P'], keywords: 'team project initiative parent', page: page('documentMove', 'Move to…') },
    { id: 'doc-pin', label: 'Pin to team…', icon: <Pin/>, keywords: 'overview resources', page: page('documentPin', 'Pin to team…') },
    ...single({ id: 'doc-apply-template', label: 'Apply document template…', icon: <LayoutTemplate/>, keywords: 'replace content', page: page('documentApplyTemplate', 'Apply document template…') }),
    ...single({ id: 'doc-new-template', label: 'New template from document…', icon: <LayoutTemplate/>, keywords: 'save as template', page: page('documentNewTemplate', 'New template from document…') }),
    { id: 'doc-delete', label: many ? 'Delete documents' : 'Delete document', icon: <Trash2/>, keywords: 'remove trash', run: () => { close(); void deleteDocumentsWithConfirm(ctx, documents, () => { if (here) host.navigate(documentParentPath(data, document)) }) } },
  ]
}

async function copyText(text: string, message: string, failure: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.success(message)
  } catch {
    toast.error(failure)
  }
}

function pageOptions({ ctx, data, docs, page: current, query, t, open, choose }: {
  ctx: DocumentActionContext
  data: BootstrapData
  docs: FlowDocument[]
  page: CommandPage | undefined
  query: string
  t: Translate
  open: (document: Pick<FlowDocument, 'slugId'>) => void
  choose: (work: () => unknown) => () => void | Promise<unknown>
}): PageOption[] {
  if (!current) return []
  const teams = viewerTeams(data)
  switch (current.id) {
    case 'documentOpen':
      return [...data.documents].filter(item => !item.archivedAt).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)).slice(0, 300).map(item => ({
        id: item.id, label: documentDisplayTitle(item, t), entity: true, keywords: `${item.title} ${item.content?.slice(0, 120) ?? ''}`, detail: parentLabel(data, item),
        icon: <DocumentGlyph document={item}/>, select: choose(() => open(item)),
      }))
    case 'documentCreateIn':
    case 'documentFromTemplateIn': {
      const templateId = current.id === 'documentFromTemplateIn' ? current.payload : undefined
      return parentOptions(data, new Set(), target => choose(() => createDocumentIn(ctx, target, templateId).then(created => { if (created) open(created) })))
    }
    case 'documentFromTemplate':
      return templateOptions(data, t, template => ({ page: page('documentFromTemplateIn', 'Create new document in…', template.id) }))
    case 'documentTeamOverview':
      return teams.map(team => ({ id: team.id, label: team.name, entity: true, keywords: team.key, icon: <TeamIcon team={team} size={16}/>, page: page('documentTeamOverviewDoc', 'Add document to team overview…', team.id) }))
    case 'documentTeamOverviewDoc': {
      const team = data.teams.find(item => item.id === current.payload)
      if (!team) return []
      const inTeam = (item: FlowDocument) => item.teamIds.includes(team.id)
      return [...data.documents].filter(item => !item.archivedAt && !documentPinnedResource(data, team.id, item.id))
        .sort((a, b) => Number(inTeam(b)) - Number(inTeam(a)) || b.updatedAt.localeCompare(a.updatedAt)).slice(0, 300)
        .map(item => ({ id: item.id, label: documentDisplayTitle(item, t), entity: true, keywords: item.title, detail: parentLabel(data, item), icon: <DocumentGlyph document={item}/>, select: choose(() => toggleDocumentPinnedToTeam(ctx, item, team)) }))
    }
    default:
  }
  const doc = docs[0]
  if (!doc) return []
  const each = (work: (item: FlowDocument) => unknown) => choose(async () => { for (const item of docs) await work(item) })
  switch (current.id) {
    case 'documentOwner': {
      const owner = docs.every(item => documentOwner(item, data.users)?.id === documentOwner(doc, data.users)?.id) ? documentOwner(doc, data.users) : undefined
      return documentOwnerOptions(ctx, doc).map(option => ({
        id: option.id, label: option.label, entity: true, group: option.groupLabel, keywords: option.id ? option.person?.name : 'unassign none remove',
        icon: option.person ? <UserAvatar className="command-avatar" avatarUrl={option.person.avatarUrl} name={option.label}/> : <NoAssigneeIcon/>,
        current: option.id === (owner?.id ?? ''), select: each(item => (option.id || null) === (documentOwner(item, data.users)?.id ?? null) ? undefined : changeDocumentOwner(ctx, item, option.id || null)),
      }))
    }
    case 'documentRemind': {
      const now = new Date()
      const format = (date: Date) => date.toLocaleString(undefined, { weekday: 'short', day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit' })
      const typed = query.trim() ? parseDate(query.trim(), now, { forwardDate: true }) : null
      return [
        ...(typed && typed.getTime() > now.getTime() ? [{ id: 'typed', label: format(typed), entity: true, forceMount: true, icon: <AlarmClock/>, select: each(item => remindAboutDocument(ctx, item, typed.toISOString())) }] : []),
        ...reminderPresetOptions(now).map(preset => ({ id: preset.id, label: preset.label, icon: <AlarmClock/>, detail: format(preset.date), select: each(item => remindAboutDocument(ctx, item, preset.date.toISOString())) })),
        { id: 'custom', label: 'Custom…', icon: <AlarmClock/>, select: choose(() => remindAboutDocumentCustom(ctx, doc)) },
      ]
    }
    case 'documentMove': {
      const parents = docs.map(item => documentParent(data, item))
      const current = new Set(parents.every(parent => parent && parent.type !== 'issue' && `${parent.type}:${parent.id}` === `${parents[0]?.type}:${parents[0]?.id}`) && parents[0] ? [`${parents[0].type}:${parents[0].id}`] : [])
      return parentOptions(data, current, target => each(item => moveDocument(ctx, item, target)))
    }
    case 'documentPin': {
      const pinned = (team: Team, item: FlowDocument) => Boolean(documentPinnedResource(data, team.id, item.id))
      // Pin every selected document unless all are pinned already (then unpin them).
      const toggleAll = async (team: Team) => { const unpin = docs.every(item => pinned(team, item)); for (const item of docs) if (pinned(team, item) === unpin) await toggleDocumentPinnedToTeam(ctx, item, team) }
      return teams.map(team => ({ id: team.id, label: team.name, entity: true, keywords: team.key, icon: <TeamIcon team={team} size={16}/>, checked: docs.every(item => pinned(team, item)) ? true : docs.some(item => pinned(team, item)) ? 'mixed' as const : false, select: () => toggleAll(team) }))
    }
    case 'documentApplyTemplate':
      return templateOptions(data, t, template => ({ select: choose(() => applyTemplateToDocument(ctx, doc, template)) }))
    case 'documentNewTemplate':
      return [
        { id: 'workspace', label: t('Workspace'), entity: true, icon: <Building2/>, select: choose(() => createTemplateFromDocument(ctx, doc, '')) },
        ...teams.map((team: Team) => ({ id: team.id, label: team.name, entity: true, keywords: team.key, icon: <TeamIcon team={team} size={16}/>, select: choose(() => createTemplateFromDocument(ctx, doc, team.id)) })),
      ]
    case 'documentSubscribers':
      return data.users.filter(user => user.active !== false && !user.app).map(user => {
        const checked = doc.subscriberIds.includes(user.id)
        return { id: user.id, label: user.displayName || user.name, entity: true, keywords: `${user.name} ${user.email ?? ''}`, icon: <UserAvatar className="command-avatar" avatarUrl={user.avatarUrl} name={user.displayName || user.name}/>, checked,
          select: () => setDocumentSubscribers(ctx, doc, checked ? doc.subscriberIds.filter(id => id !== user.id) : [...doc.subscriberIds, user.id]) }
      })
    default:
      return []
  }
}
