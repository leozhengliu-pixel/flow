/**
 * Shared document actions. Every surface that offers document commands (the
 * document page "…" menu, list row menus, project/initiative resource menus,
 * ⌘K) calls these helpers so behaviour, toasts and wording stay identical.
 *
 * A document has a single parent (team, project, initiative or issue). The
 * data model still stores teamIds/projectIds/issueId plus initiative resource
 * links, so "Move to" replaces all of them with the chosen parent.
 */
import { toast } from 'sonner'

import { confirmAction, promptAction, promptDateAction } from '@/components/ui/action-dialog-service'
import {
  createDocument, createDocumentReminder, createDocumentTemplate, createInitiativeResource, deleteDocument,
  deleteInitiativeResource, deleteTeamResource, pinTeamResource, replaceDocumentPermissions, restoreTrashEntry, updateDocument,
} from '@/lib/api'
import { toggleFavoriteFor } from '@/lib/favorites'
import { documentPath } from '@/lib/app-routes'
import type { BootstrapData, DocumentPermission, DocumentTemplate, FlowDocument, Initiative, Project, Team, User } from '@/types/flow'

export type Translate = (source: string) => string

/** What an action needs from the surface that triggers it. */
export interface DocumentActionContext {
  data: BootstrapData
  /** Refresh workspace metadata after a write. */
  reload: () => Promise<void>
  navigate: (path: string) => void
  t: Translate
}

/* ---------- pure helpers ---------- */

/** Documents created from "New document" start untitled; lists show a fallback. */
export function documentDisplayTitle(document: Pick<FlowDocument, 'title'>, t: Translate) {
  return document.title.trim() || t('Untitled')
}

export function documentAbsoluteUrl(workspaceSlug: string, document: Pick<FlowDocument, 'slugId'>) {
  return new URL(documentPath(workspaceSlug, document), typeof location === 'undefined' ? 'http://localhost' : location.origin).href
}

/**
 * The document owner: the user holding the "owner" permission. Documents with
 * an explicit permission list and no owner entry have no owner ("No owner");
 * legacy documents without permissions are owned by their creator.
 */
export function documentOwner(document: Pick<FlowDocument, 'permissions' | 'creator'>, users: User[]): User | undefined {
  const permissions = document.permissions ?? []
  if (!permissions.length) return document.creator
  const owner = permissions.find(permission => permission.role === 'owner' && permission.subjectType === 'user')
  if (!owner) return undefined
  return users.find(user => user.id === owner.subjectId) ?? (owner.subjectId === document.creator.id ? document.creator : undefined)
}

/** Teams the viewer belongs to ("My teams"). */
export function viewerTeams(data: BootstrapData): Team[] {
  const ids = new Set((data.teamMembers ?? []).filter(member => member.userId === data.viewer.id).map(member => member.teamId))
  return data.teams.filter(team => ids.has(team.id) && !team.archivedAt)
}

export function documentInitiatives(data: BootstrapData, document: Pick<FlowDocument, 'id'>): Initiative[] {
  return data.initiatives.filter(initiative => (initiative.resources ?? []).some(resource => resource.documentId === document.id))
}

export type DocumentParent =
  | { type: 'issue'; id: string }
  | { type: 'initiative'; id: string; initiative: Initiative }
  | { type: 'project'; id: string; project: Project }
  | { type: 'team'; id: string; team: Team }

/** The single parent shown in breadcrumbs and checked in "Move to" (issue > initiative > project > team). */
export function documentParent(data: BootstrapData, document: FlowDocument): DocumentParent | undefined {
  if (document.issueId) return { type: 'issue', id: document.issueId }
  const initiative = documentInitiatives(data, document)[0]
  if (initiative) return { type: 'initiative', id: initiative.id, initiative }
  const project = data.projects.find(item => document.projectIds.includes(item.id))
  if (project) return { type: 'project', id: project.id, project }
  const team = data.teams.find(item => document.teamIds.includes(item.id))
  if (team) return { type: 'team', id: team.id, team }
  return undefined
}

/** `# Title` followed by the Markdown body, as copied/downloaded. */
export function documentMarkdown(document: Pick<FlowDocument, 'title' | 'content'>, t: Translate = value => value) {
  const title = document.title.trim() || t('Untitled')
  const body = document.content ?? ''
  return body.trim() ? `# ${title}\n\n${body.replace(/\s+$/, '')}\n` : `# ${title}\n`
}

export function documentFileName(document: Pick<FlowDocument, 'title'>, extension: string, t: Translate = value => value) {
  // oxlint-disable-next-line no-control-regex -- stripping control characters from file names is intentional
  const base = (document.title.trim() || t('Untitled')).replace(/[\\/:*?"<>|\u0000-\u001f]+/g, ' ').replace(/\s+/g, ' ').trim().slice(0, 120) || 'document'
  return `${base}.${extension}`
}

/** Client-side Markdown export; returns the filename that was offered. */
export function downloadDocumentMarkdown(document: Pick<FlowDocument, 'title' | 'content'>, t: Translate = value => value) {
  const name = documentFileName(document, 'md', t)
  const url = URL.createObjectURL(new Blob([documentMarkdown(document, t)], { type: 'text/markdown;charset=utf-8' }))
  const link = globalThis.document.createElement('a')
  link.href = url
  link.download = name
  link.rel = 'noopener'
  globalThis.document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  return name
}

/** PDF export uses the browser print dialog with the document print stylesheet. */
export function printDocument(document: Pick<FlowDocument, 'title'>, t: Translate = value => value) {
  const previous = globalThis.document.title
  globalThis.document.title = document.title.trim() || t('Untitled')
  const restore = () => { globalThis.document.title = previous; window.removeEventListener('afterprint', restore) }
  window.addEventListener('afterprint', restore)
  window.print()
}

/* ---------- writes ---------- */

const message = (error: unknown, fallback: string) => error instanceof Error && error.message ? error.message : fallback

async function guarded(ctx: DocumentActionContext, work: () => Promise<unknown>, success: string | undefined, failure: string) {
  try {
    await work()
    await ctx.reload()
    if (success) toast.success(success)
    return true
  } catch (error) {
    toast.error(message(error, ctx.t(failure)))
    return false
  }
}

async function copy(ctx: DocumentActionContext, value: string, success: string) {
  try {
    await navigator.clipboard.writeText(value)
    toast.success(ctx.t(success))
  } catch {
    toast.error(ctx.t('Could not copy to clipboard'))
  }
}

export const copyDocumentUrl = (ctx: DocumentActionContext, document: FlowDocument) =>
  copy(ctx, documentAbsoluteUrl(ctx.data.workspace.urlKey, document), 'Copied document link to clipboard')
export const copyDocumentTitle = (ctx: DocumentActionContext, document: FlowDocument) =>
  copy(ctx, documentDisplayTitle(document, ctx.t), 'Copied document title to clipboard')
export const copyDocumentTitleAsLink = (ctx: DocumentActionContext, document: FlowDocument) =>
  copy(ctx, `[${documentDisplayTitle(document, ctx.t)}](${documentAbsoluteUrl(ctx.data.workspace.urlKey, document)})`, 'Copied document title as link to clipboard')
export const copyDocumentMarkdown = (ctx: DocumentActionContext, document: FlowDocument) =>
  copy(ctx, document.content ?? '', 'Copied document content as Markdown to clipboard')

export function toggleDocumentFavorite(ctx: DocumentActionContext, document: FlowDocument) {
  const favorited = ctx.data.favorites.some(item => item.userId === ctx.data.viewer.id && item.resourceType === 'document' && item.resourceId === document.id) || document.favorite
  return toggleFavoriteFor(ctx.data, 'document', document.id, !favorited, favorited)
}

export async function renameDocument(ctx: DocumentActionContext, document: FlowDocument) {
  const next = await promptAction(ctx.t('Rename document'), document.title, { confirmLabel: ctx.t('Save') })
  const value = next?.trim()
  if (value === undefined || value === document.title) return false
  return guarded(ctx, () => updateDocument(document.id, { title: value }), undefined, 'Could not rename document')
}

export async function deleteDocumentWithConfirm(ctx: DocumentActionContext, document: FlowDocument, afterDelete?: () => void) {
  const title = documentDisplayTitle(document, ctx.t)
  const confirmed = await confirmAction(ctx.t('Delete "{name}"?').replace('{name}', title), {
    description: ctx.t('Deleted documents are available in the "Recently deleted" view for 30 days, before they are permanently deleted.'),
    confirmLabel: ctx.t('Delete'),
  })
  if (!confirmed) return false
  const done = await guarded(ctx, () => deleteDocument(document.id), undefined, 'Could not delete document')
  if (done) afterDelete?.()
  return done
}

/** Deletes one or several documents after a single confirmation (a list selection can hold many). */
export async function deleteDocumentsWithConfirm(ctx: DocumentActionContext, documents: FlowDocument[], afterDelete?: () => void) {
  if (documents.length === 1) return deleteDocumentWithConfirm(ctx, documents[0], afterDelete)
  const confirmed = await confirmAction(ctx.t('Delete {count} documents?').replace('{count}', String(documents.length)), {
    description: ctx.t('Deleted documents are available in the "Recently deleted" view for 30 days, before they are permanently deleted.'),
    confirmLabel: ctx.t('Delete'),
  })
  if (!confirmed) return false
  const done = await guarded(ctx, async () => { for (const item of documents) await deleteDocument(item.id) }, undefined, 'Could not delete document')
  if (done) afterDelete?.()
  return done
}

/** Restores a deleted document from the trash (the page stays on its URL). */
export async function restoreDeletedDocument(ctx: DocumentActionContext, trashEntryId: string) {
  return guarded(ctx, () => restoreTrashEntry(trashEntryId), ctx.t('Document restored'), 'Could not restore document')
}

export async function duplicateDocument(ctx: DocumentActionContext, document: FlowDocument) {
  const title = documentDisplayTitle(document, ctx.t)
  try {
    const created = await createDocument({
      title: document.title.trim() ? `${title} (${ctx.t('copy')})` : '',
      icon: document.icon, color: document.color, content: document.content, contentState: document.contentState, contentData: document.contentData,
      projectIds: document.projectIds, teamIds: document.teamIds, issueId: document.issueId,
    })
    await ctx.reload()
    toast.success(ctx.t('Document duplicated'))
    return created
  } catch (error) {
    toast.error(message(error, ctx.t('Could not duplicate document')))
    return undefined
  }
}

/** `teamId: ''` creates a workspace-wide template. */
export async function createTemplateFromDocument(ctx: DocumentActionContext, document: FlowDocument, teamId: string) {
  const title = documentDisplayTitle(document, ctx.t)
  return guarded(ctx, () => createDocumentTemplate({ teamId, name: title, title: document.title.trim(), icon: document.icon, content: document.content, contentState: document.contentState, contentData: document.contentData }), ctx.t('Template created'), 'Could not create template')
}

/**
 * Replaces the document's title, icon and body with a template's. The body goes
 * as Markdown only: the server then starts a new realtime generation and the
 * open editor reloads from it (sending editor state would not reach an editor
 * that is already mounted on the old collaborative document).
 */
export async function applyTemplateToDocument(ctx: DocumentActionContext, document: FlowDocument, template: DocumentTemplate) {
  return guarded(ctx, () => updateDocument(document.id, {
    title: template.title ?? '', icon: template.icon ?? '', content: template.content ?? '',
  }), ctx.t('Template applied'), 'Could not apply template')
}

export type DocumentMoveTarget = { type: 'team' | 'project' | 'initiative'; id: string }

/** Replaces the document's parent: it ends up under exactly one team, project or initiative. */
export async function moveDocument(ctx: DocumentActionContext, document: FlowDocument, target: DocumentMoveTarget) {
  const name = target.type === 'team' ? ctx.data.teams.find(item => item.id === target.id)?.name
    : target.type === 'project' ? ctx.data.projects.find(item => item.id === target.id)?.name
      : ctx.data.initiatives.find(item => item.id === target.id)?.name
  return guarded(ctx, async () => {
    const linked = documentInitiatives(ctx.data, document)
    for (const initiative of linked) {
      if (target.type === 'initiative' && initiative.id === target.id) continue
      for (const resource of (initiative.resources ?? []).filter(item => item.documentId === document.id)) await deleteInitiativeResource(initiative.id, resource.id)
    }
    await updateDocument(document.id, {
      teamIds: target.type === 'team' ? [target.id] : [],
      projectIds: target.type === 'project' ? [target.id] : [],
      issueId: '',
    })
    if (target.type === 'initiative' && !linked.some(initiative => initiative.id === target.id)) {
      await createInitiativeResource(target.id, { type: 'document', documentId: document.id, title: document.title || undefined })
    }
  }, ctx.t('Moved to {name}').replace('{name}', name ?? ''), 'Could not move document')
}

/**
 * Creates an untitled document under one parent (team, project or initiative),
 * optionally from a template, and returns it (undefined after a toast on failure).
 */
export async function createDocumentIn(ctx: DocumentActionContext, target: DocumentMoveTarget, templateId?: string) {
  try {
    const created = await createDocument({
      title: '',
      teamIds: target.type === 'team' ? [target.id] : [],
      projectIds: target.type === 'project' ? [target.id] : [],
      ...(templateId ? { templateId } : {}),
    })
    if (target.type === 'initiative') await createInitiativeResource(target.id, { type: 'document', documentId: created.id })
    await ctx.reload()
    return created
  } catch (error) {
    toast.error(message(error, ctx.t('Could not create document')))
    return undefined
  }
}

export function documentPinnedResource(data: BootstrapData, teamId: string, documentId: string) {
  return (data.teamPinnedResources ?? []).find(resource => resource.teamId === teamId && resource.resourceType === 'document' && resource.resourceId === documentId)
}

/** Pins the document to a team's overview "Team resources" (or unpins it when already pinned). */
export async function toggleDocumentPinnedToTeam(ctx: DocumentActionContext, document: FlowDocument, team: Team) {
  const existing = documentPinnedResource(ctx.data, team.id, document.id)
  return guarded(ctx, () => existing
    ? deleteTeamResource(team.id, existing.id)
    : pinTeamResource(team.id, { resourceType: 'document', resourceId: document.id, title: document.title }),
  ctx.t(existing ? 'Removed from team overview' : 'Pinned to team overview'), 'Could not update team overview')
}

export async function remindAboutDocument(ctx: DocumentActionContext, document: FlowDocument, remindAt: string) {
  try {
    await createDocumentReminder(document.id, remindAt)
    toast.success(ctx.t('Reminder set'))
    return true
  } catch (error) {
    toast.error(message(error, ctx.t('Could not set reminder')))
    return false
  }
}

/** The "Custom…" reminder: asks for a date and time in a dialog, then sets it. */
export async function remindAboutDocumentCustom(ctx: DocumentActionContext, document: FlowDocument) {
  const value = await promptDateAction(ctx.t('Remind me'), '', { confirmLabel: ctx.t('Set reminder'), min: new Date().toISOString() })
  if (!value) return false
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getTime() <= Date.now()) { toast.error(ctx.t('Choose a time in the future')); return false }
  return remindAboutDocument(ctx, document, date.toISOString())
}

/**
 * Sets (or clears, with `null`) the document owner. Other former owners keep
 * edit access; the permission list is replaced as a whole.
 */
export async function changeDocumentOwner(ctx: DocumentActionContext, document: FlowDocument, userId: string | null) {
  type Entry = Pick<DocumentPermission, 'subjectType' | 'subjectId' | 'role'>
  const current: Entry[] = (document.permissions ?? []).map(({ subjectType, subjectId, role }) => ({ subjectType, subjectId, role }))
  // A document that has no permissions list yet is owned by its creator: keep the creator on the list.
  if (!current.length) current.push({ subjectType: 'user', subjectId: document.creator.id, role: 'owner' })
  const next: Entry[] = current.map(item => item.role === 'owner' && item.subjectType === 'user' && item.subjectId !== userId ? { ...item, role: 'editor' } : item)
  if (userId) {
    const index = next.findIndex(item => item.subjectType === 'user' && item.subjectId === userId)
    if (index >= 0) next[index] = { ...next[index], role: 'owner' }
    else next.push({ subjectType: 'user', subjectId: userId, role: 'owner' })
  }
  return guarded(ctx, () => replaceDocumentPermissions(document.id, next), undefined, 'Could not change owner')
}

/** Replaces the subscriber list (the creator keeps their access, only notifications change). */
export async function setDocumentSubscribers(ctx: DocumentActionContext, document: FlowDocument, subscriberIds: string[]) {
  return guarded(ctx, () => updateDocument(document.id, { subscriberIds }), undefined, 'Could not update document subscription')
}

/* ---------- surface-only actions (history, authors, subscribers, owner) ---------- */

export type DocumentUiAction = 'history' | 'authors' | 'subscribers' | 'owner'
export const DOCUMENT_UI_EVENT = 'flow:document-ui-action'
export interface DocumentUiActionDetail { action: DocumentUiAction; documentId: string; handled: boolean }

/**
 * Asks the open document page to run a page-level action. Returns false when
 * no page for `document` is mounted (callers then navigate to the page).
 */
export function requestDocumentUiAction(action: DocumentUiAction, documentId: string) {
  const detail: DocumentUiActionDetail = { action, documentId, handled: false }
  window.dispatchEvent(new CustomEvent(DOCUMENT_UI_EVENT, { detail }))
  return detail.handled
}
