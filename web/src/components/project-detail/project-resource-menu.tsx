import { Fragment, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { MoreVertical, Star } from 'lucide-react'
import { toast } from 'sonner'
import { TeamIcon } from '@/components/issue/issue-icons'
import { confirmAction, promptAction } from '@/components/ui/action-dialog-service'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { toggleFavoriteFor } from '@/lib/favorites'
import { createDocument, createDocumentTemplate, deleteDocument, updateDocument } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, FlowDocument, Project, ProjectResource, Team } from '@/types/flow'
import { ProjectMenuItem, ProjectMenuShortcut, ProjectSubmenu } from './project-menu-primitives'
import { resourceDisplayTitle } from './project-resource-link-name'

type ResourceUpdate = (id: string, input: { pinnedTeamIds?: string[] }) => Promise<ProjectResource>

/** Absolute URL for a resource (document resources carry an app-relative path). */
function resourceAbsoluteURL(url: string) {
  try { return new URL(url, window.location.origin).href } catch { return url }
}

const copyText = (value: string, message: string) => void navigator.clipboard.writeText(value).then(() => toast.success(message), () => toast.error('Could not copy to clipboard'))

/**
 * The "⋮" menu on a project Resources pill. Links get Linear's short link menu
 * (Copy link, Pin to team, Edit, Delete); documents get the document context
 * menu (Move to, Pin to team, Duplicate, New template, Rename…, Favorite, Copy,
 * Show document history, Delete).
 */
export function ProjectResourceMenu({ data, document, onDeleteLink, onEditLink, onOpenDocumentHistory, onReload, onUpdate, project, projects, resource, teams }: {
  data?: BootstrapData
  document?: FlowDocument
  onDeleteLink: () => void
  onEditLink: () => void
  onOpenDocumentHistory?: (document: FlowDocument) => void
  onReload?: () => Promise<void>
  onUpdate: ResourceUpdate
  project?: Project
  projects?: Project[]
  resource: ProjectResource
  teams: Team[]
}) {
  const kind = document ? 'document' : 'link'
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild><button aria-label={`${resourceDisplayTitle(resource)} actions`} data-i18n-ignore type="button"><MoreVertical size={14}/></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="end" className="project-action-menu project-resource-action-menu" data-kind={kind} collisionPadding={16} sideOffset={4}>
      {document
        ? <DocumentResourceItems data={data} document={document} onOpenDocumentHistory={onOpenDocumentHistory} onReload={onReload} onUpdate={onUpdate} project={project} projects={projects} resource={resource} teams={teams}/>
        : <>
          <ProjectMenuItem icon={null} label="Copy link" onSelect={() => copyText(resourceAbsoluteURL(resource.url), 'Resource link copied')}/>
          <PinToTeam onUpdate={onUpdate} resource={resource} teams={teams}/>
          <ProjectMenuItem icon={null} label="Edit" onSelect={onEditLink}/>
          <ProjectMenuItem icon={null} label="Delete" onSelect={onDeleteLink}/>
        </>}
    </DropdownMenu.Content></DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function PinToTeam({ onUpdate, resource, teams }: { onUpdate: ResourceUpdate; resource: ProjectResource; teams: Team[] }) {
  const pinned = resource.pinnedTeamIds ?? []
  return <ProjectSubmenu label="Pin to team" icon={null} className="project-resource-submenu">
    {teams.map(team => {
      const checked = pinned.includes(team.id)
      return <DropdownMenu.CheckboxItem checked={checked} key={team.id} onCheckedChange={() => void onUpdate(resource.id, { pinnedTeamIds: checked ? pinned.filter(id => id !== team.id) : [...pinned, team.id] })}>
        <span className="project-menu-checkbox project-resource-menu__check" data-checked={checked || undefined}>{checked && <CheckboxMark/>}</span>
        <TeamRow team={team}/>
      </DropdownMenu.CheckboxItem>
    })}
    {!teams.length && <div className="project-action-menu__empty">No teams</div>}
  </ProjectSubmenu>
}

function TeamRow({ team }: { team: Team }) {
  return <><span className="project-menu-icon project-resource-menu__team-icon"><TeamIcon team={team} size={14}/></span><span className="project-menu-label project-resource-menu__team" data-i18n-ignore>{team.name}<small>{team.key}</small></span></>
}

function GroupLabel({ children }: { children: ReactNode }) {
  const { t } = useI18n()
  return <DropdownMenu.Label className="project-resource-menu__group">{typeof children === 'string' ? t(children) : children}</DropdownMenu.Label>
}

function DocumentResourceItems({ data, document, onOpenDocumentHistory, onReload, onUpdate, project, projects = [], resource, teams }: {
  data?: BootstrapData
  document: FlowDocument
  onOpenDocumentHistory?: (document: FlowDocument) => void
  onReload?: () => Promise<void>
  onUpdate: ResourceUpdate
  project?: Project
  projects?: Project[]
  resource: ProjectResource
  teams: Team[]
}) {
  const { t } = useI18n()
  const reload = () => onReload?.() ?? Promise.resolve()
  const run = (action: () => Promise<unknown>, success: string, failure: string) => void action().then(reload).then(() => { if (success) toast.success(success) }, error => toast.error(failure, { description: error instanceof Error ? error.message : undefined }))
  const title = document.title || resource.title || 'Untitled document'
  const url = resourceAbsoluteURL(resource.url)
  const favorited = Boolean(data?.favorites?.some(item => item.userId === data.viewer.id && item.resourceType === 'document' && item.resourceId === document.id)) || document.favorite
  const otherProjects = projects.filter(item => item.id !== project?.id && !item.archivedAt)
  const moveToTeam = (team: Team) => run(() => updateDocument(document.id, { teamIds: [team.id], projectIds: [] }), `Moved to ${team.name}`, 'Could not move document')
  const moveToProject = (target: Project) => run(() => updateDocument(document.id, { projectIds: [target.id] }), `Moved to ${target.name}`, 'Could not move document')
  const duplicate = () => run(() => createDocument({ title: `${title} (copy)`, icon: document.icon, color: document.color, content: document.content, contentState: document.contentState, contentData: document.contentData, projectIds: document.projectIds, teamIds: document.teamIds }), 'Document duplicated', 'Could not duplicate document')
  const createTemplate = (team: Team) => run(() => createDocumentTemplate({ teamId: team.id, name: title, title, icon: document.icon, content: document.content, contentState: document.contentState, contentData: document.contentData }), 'Template created', 'Could not create template')
  const rename = () => void promptAction('Rename document', title, { confirmLabel: 'Save' }).then(next => {
    const value = next?.trim()
    if (value && value !== document.title) run(() => updateDocument(document.id, { title: value }), '', 'Could not rename document')
  })
  const remove = () => void confirmAction(`Delete "${title}"?`, { description: 'Deleted documents are available in the "Recently deleted" view for 30 days, before they are permanently deleted.', confirmLabel: 'Delete' }).then(confirmed => {
    if (confirmed) run(() => deleteDocument(document.id), '', 'Could not delete document')
  })
  return <>
    <ProjectSubmenu label="Move to" icon={null} shortcut="⇧ P" className="project-resource-submenu">
      <GroupLabel>My teams</GroupLabel>
      {teams.map(team => <DropdownMenu.Item key={team.id} onSelect={() => moveToTeam(team)}><TeamRow team={team}/></DropdownMenu.Item>)}
      {otherProjects.length > 0 && <><GroupLabel>Projects</GroupLabel>{otherProjects.map(item => <DropdownMenu.Item key={item.id} onSelect={() => moveToProject(item)}><span className="project-menu-icon project-resource-menu__team-icon"><ViewGlyph color={item.color} icon={normalizeProjectIcon(item.icon)} style={{ width: 14, height: 14 }}/></span><span className="project-menu-label" data-i18n-ignore>{item.name}</span></DropdownMenu.Item>)}</>}
    </ProjectSubmenu>
    <PinToTeam onUpdate={onUpdate} resource={resource} teams={teams}/>
    <ProjectMenuItem icon={null} label="Duplicate" onSelect={duplicate}/>
    <ProjectSubmenu label="New template from document" icon={null} className="project-resource-submenu">
      {teams.map(team => <DropdownMenu.Item key={team.id} onSelect={() => createTemplate(team)}><TeamRow team={team}/></DropdownMenu.Item>)}
    </ProjectSubmenu>
    <ProjectMenuItem icon={null} label="Rename…" shortcut="⇧ R" onSelect={rename}/>
    <DropdownMenu.Separator/>
    {data && (favorited
      ? <DropdownMenu.Item onSelect={() => void toggleFavoriteFor(data, 'document', document.id, false, true)}><span className="project-menu-icon" data-favorited=""><Star aria-hidden="true" fill="currentColor" size={16} strokeWidth={1.5}/></span><span className="project-menu-label">{t('Remove from favorites')}</span><ProjectMenuShortcut value="⌥ F"/></DropdownMenu.Item>
      : <ProjectMenuItem icon={null} label="Favorite" shortcut="⌥ F" onSelect={() => void toggleFavoriteFor(data, 'document', document.id, true, false)}/>)}
    <ProjectSubmenu label="Copy" icon={null} className="project-resource-submenu project-resource-copy-menu">
      {([
        ['Copy URL', '⌘ ⇧ ,', url, 'Document URL copied'],
        ['Copy title', "⌘ ⇧ '", title, 'Document title copied'],
        ['Copy title as link', '⌘ C', `[${title}](${url})`, 'Document title and link copied'],
        ['Copy content as Markdown', '⌘ ⌥ C', document.content ?? '', 'Document content copied'],
      ] as const).map(([label, shortcut, value, message]) => <Fragment key={label}><ProjectMenuItem icon={null} label={label} shortcut={shortcut} onSelect={() => copyText(value, message)}/></Fragment>)}
    </ProjectSubmenu>
    <DropdownMenu.Separator/>
    {onOpenDocumentHistory && <ProjectMenuItem icon={null} label="Show document history" onSelect={() => onOpenDocumentHistory(document)}/>}
    <ProjectMenuItem icon={null} label="Delete" onSelect={remove}/>
  </>
}
