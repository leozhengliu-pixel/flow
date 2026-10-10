/**
 * The document menu: one set of rows rendered inside a right-click menu
 * (list rows), a "…" dropdown (document page, resource pills) or either
 * surface of the shared row-menu kit. Wording and order follow Linear's:
 *
 *   Move to ▸ · Pin to overview / Pin to team ▸ · Duplicate ·
 *   New template from document ▸ · Rename…
 *   ─ Favorite · Copy ▸ · Download ▸ (page) · Remind me ▸
 *   ─ Show author names (page) · Show document history · People with access (page)
 *   ─ Delete
 */
import { Building2 } from 'lucide-react'
import { useRef, type MouseEvent, type ReactElement, type ReactNode } from 'react'

import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { TeamIcon } from '@/components/issue/issue-icons'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearReminderOptions } from '@/components/ui/reminder-options'
import {
  LinearContextMenuPortal, LinearContextMenuRoot, LinearContextMenuTrigger, LinearMenuContent, LinearMenuItem, LinearMenuOptions,
  LinearMenuSeparator, LinearSubmenu, type LinearMenuOption,
} from '@/components/ui/row-context-menu'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { documentPath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { FlowDocument, Team } from '@/types/flow'
import { documentTemplateOptions } from './document-template-options'
import {
  applyTemplateToDocument, copyDocumentMarkdown, copyDocumentTitle, copyDocumentTitleAsLink, copyDocumentUrl, createTemplateFromDocument, deleteDocumentWithConfirm,
  documentParent, documentPinnedResource, downloadDocumentMarkdown, duplicateDocument, moveDocument, printDocument, remindAboutDocument,
  remindAboutDocumentCustom, renameDocument, requestDocumentUiAction, toggleDocumentFavorite, toggleDocumentPinnedToTeam, viewerTeams,
  type DocumentActionContext, type DocumentUiAction,
} from './document-actions'

/** Key hints that open a hovered list row's menu and run the matching row. */
export const DOCUMENT_ROW_SHORTCUTS = ['⇧ P', '⇧ R', '⌥ F', '⇧ H', '⌘ ⇧ ,', "⌘ ⇧ '", '⌘ C', '⌘ ⌥ C'] as const

export interface DocumentMenuItemsProps {
  ctx: DocumentActionContext
  document: FlowDocument
  /** `page` adds Download, Show author names and People with access; `row` pins to `team`'s overview. */
  variant: 'page' | 'row' | 'resource'
  /** The team whose overview "Pin to overview" targets (list rows). */
  team?: Team
  /** Page-only: state and handlers for the author names toggle and the access dialog. */
  showAuthorNames?: boolean
  onToggleAuthorNames?: () => void
  onOpenAccess?: () => void
  /** Overrides the default "open the page's UI action, else navigate" behaviour. */
  onUiAction?: (action: DocumentUiAction) => void
  canDelete?: boolean
  /** Runs after the document is deleted (the page navigates away). */
  onDeleted?: () => void
}

export function DocumentMenuItems({ ctx, document, variant, team, showAuthorNames, onToggleAuthorNames, onOpenAccess, onUiAction, canDelete = true, onDeleted }: DocumentMenuItemsProps) {
  const { t } = useI18n()
  const { data } = ctx
  const favorited = data.favorites.some(item => item.userId === data.viewer.id && item.resourceType === 'document' && item.resourceId === document.id) || document.favorite
  const teams = viewerTeams(data)
  const parent = documentParent(data, document)
  const ui = (action: DocumentUiAction) => {
    if (onUiAction) return onUiAction(action)
    if (!requestDocumentUiAction(action, document.id)) ctx.navigate(`${documentPath(data.workspace.urlKey, document)}${action === 'history' ? '?history' : ''}`)
  }
  const moveOptions: LinearMenuOption[] = [
    ...teams.map(item => ({ id: `team:${item.id}`, label: item.name, translate: false, group: 'My teams', icon: <TeamIcon team={item} size={14}/>, keywords: item.key })),
    ...data.projects.filter(project => !project.archivedAt).map(project => ({ id: `project:${project.id}`, label: project.name, translate: false, group: 'Projects', icon: <ViewGlyph color={project.color} icon={normalizeProjectIcon(project.icon)} style={{ width: 14, height: 14 }}/> })),
    ...data.initiatives.map(initiative => ({ id: `initiative:${initiative.id}`, label: initiative.name, translate: false, group: 'Initiatives', icon: <ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'} style={{ width: 14, height: 14 }}/> })),
  ]
  const moveSelected = new Set(parent && parent.type !== 'issue' ? [`${parent.type}:${parent.id}`] : [])
  const templateMenu = documentTemplateOptions(ctx, document)
  const pinnedTeam = team ?? teams[0]
  const rowPinned = pinnedTeam ? Boolean(documentPinnedResource(data, pinnedTeam.id, document.id)) : false
  const pinSelected = new Set(teams.filter(item => documentPinnedResource(data, item.id, document.id)).map(item => item.id))
  return <>
    <LinearSubmenu icon={<IssueActionGlyph label="Move to" fallback={null}/>} label="Move to" shortcut="⇧ P" search>
      {({ close }) => <LinearMenuOptions options={moveOptions} selected={moveSelected} keepOpen={false} placeholder="Move to…" emptyLabel="No results" onChoose={id => {
        const [type, ...rest] = id.split(':')
        close()
        void moveDocument(ctx, document, { type: type as 'team' | 'project' | 'initiative', id: rest.join(':') })
      }}/>}
    </LinearSubmenu>
    {variant === 'row' && pinnedTeam
      ? <LinearMenuItem icon={<IssueActionGlyph label="Pin to team" fallback={null}/>} label={rowPinned ? 'Remove from overview' : 'Pin to overview'} onSelect={() => void toggleDocumentPinnedToTeam(ctx, document, pinnedTeam)}/>
      : <LinearSubmenu icon={<IssueActionGlyph label="Pin to team" fallback={null}/>} label="Pin to team">
        <LinearMenuOptions options={teams.map(item => ({ id: item.id, label: item.name, translate: false, icon: <TeamIcon team={item} size={14}/> }))} selected={pinSelected} multiple emptyLabel="No teams" onChoose={id => { const target = teams.find(item => item.id === id); if (target) void toggleDocumentPinnedToTeam(ctx, document, target) }}/>
      </LinearSubmenu>}
    <LinearMenuItem icon={<IssueActionGlyph label="Duplicate" fallback={null}/>} label="Duplicate" onSelect={() => void duplicateDocument(ctx, document).then(created => { if (created && variant === 'page') ctx.navigate(documentPath(data.workspace.urlKey, created)) })}/>
    <LinearSubmenu icon={<IssueActionGlyph label="New template from document" fallback={null}/>} label="New template from document">
      <LinearMenuItem icon={<Building2 size={14}/>} label="Workspace" onSelect={() => void createTemplateFromDocument(ctx, document, '')}/>
      {teams.length > 0 && <LinearMenuSeparator/>}
      {teams.map(item => <LinearMenuItem key={item.id} icon={<TeamIcon team={item} size={14}/>} label={item.name} translate={false} onSelect={() => void createTemplateFromDocument(ctx, document, item.id)}/>)}
    </LinearSubmenu>
    {variant !== 'resource' && templateMenu.options.length > 0 && <LinearSubmenu icon={<IssueActionGlyph label="New template from document" fallback={null}/>} label="Apply document template…" search>
      {({ close }) => <LinearMenuOptions options={templateMenu.options.map(option => ({ id: option.id, label: option.label, translate: false, group: option.groupLabel, icon: option.icon, keywords: option.keywords }))} selected={new Set<string>()} keepOpen={false} placeholder="Apply template…" emptyLabel="No templates" onChoose={id => {
        const template = templateMenu.byId.get(id)
        close()
        if (template) void applyTemplateToDocument(ctx, document, template)
      }}/>}
    </LinearSubmenu>}
    <LinearMenuItem icon={<IssueActionGlyph label="Rename…" fallback={null}/>} label="Rename…" shortcut="⇧ R" onSelect={() => void renameDocument(ctx, document)}/>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="favorite"/>} label={favorited ? 'Remove from favorites' : 'Favorite'} shortcut="⌥ F" onSelect={() => void toggleDocumentFavorite(ctx, document)}/>
    <LinearSubmenu icon={<IssueActionGlyph label="Copy" fallback={null}/>} label="Copy">
      <LinearMenuItem icon={<IssueActionGlyph label="Copy URL" fallback={null}/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={() => void copyDocumentUrl(ctx, document)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy title" fallback={null}/>} label="Copy title" shortcut="⌘ ⇧ '" onSelect={() => void copyDocumentTitle(ctx, document)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy title as link" fallback={null}/>} label="Copy title as link" shortcut="⌘ C" onSelect={() => void copyDocumentTitleAsLink(ctx, document)}/>
      <LinearMenuItem icon={<IssueActionGlyph label="Copy content as Markdown" fallback={null}/>} label="Copy content as Markdown" shortcut="⌘ ⌥ C" onSelect={() => void copyDocumentMarkdown(ctx, document)}/>
    </LinearSubmenu>
    {variant === 'page' && <LinearSubmenu icon={<LinearGlyph name="exportCsv"/>} label="Download">
      <LinearMenuItem label="Markdown" onSelect={() => { downloadDocumentMarkdown(document, t) }}/>
      <LinearMenuItem label="PDF" onSelect={() => printDocument(document, t)}/>
    </LinearSubmenu>}
    <LinearSubmenu icon={<ViewGlyph icon="Alarm" color="currentColor"/>} label="Remind me" shortcut="⇧ H" search>
      {({ close }) => <LinearReminderOptions onChoose={date => { close(); void remindAboutDocument(ctx, document, date.toISOString()) }} onCustom={() => void remindAboutDocumentCustom(ctx, document)}/>}
    </LinearSubmenu>
    <LinearMenuSeparator/>
    {variant === 'page' && onToggleAuthorNames && <LinearMenuItem icon={<LinearGlyph name="creator"/>} label={showAuthorNames ? 'Hide author names' : 'Show author names'} shortcut="⇧ A" onSelect={onToggleAuthorNames}/>}
    <LinearMenuItem icon={<IssueActionGlyph label="Show document history" fallback={null}/>} label="Show document history" onSelect={() => ui('history')}/>
    {variant === 'page' && onOpenAccess && <LinearMenuItem icon={<LinearGlyph name="members"/>} label="People with access" onSelect={onOpenAccess}/>}
    <LinearMenuSeparator/>
    {canDelete && <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={() => void deleteDocumentWithConfirm(ctx, document, onDeleted)}/>}
  </>
}

/** Right-click menu for a document row; the row element is the trigger (its child). */
export function DocumentRowMenu({ children, ctx, document, team, canDelete, extra }: { children: ReactElement; ctx: DocumentActionContext; document: FlowDocument; team?: Team; canDelete?: boolean; /** Surface-specific rows appended after the document rows. */ extra?: ReactNode }) {
  const { t } = useI18n()
  const point = useRef({ x: 0, y: 0 })
  return <LinearContextMenuRoot>
    <LinearContextMenuTrigger asChild onContextMenu={(event: MouseEvent) => { point.current = { x: event.clientX, y: event.clientY } }}>{children}</LinearContextMenuTrigger>
    <LinearContextMenuPortal>
      <LinearMenuContent label={t('Document actions')}>
        <DocumentMenuItems ctx={ctx} document={document} variant="row" team={team} canDelete={canDelete}/>
        {extra}
      </LinearMenuContent>
    </LinearContextMenuPortal>
  </LinearContextMenuRoot>
}
