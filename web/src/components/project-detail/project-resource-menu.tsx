import { useMemo } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { MoreVertical } from 'lucide-react'
import { toast } from 'sonner'
import { TeamIcon } from '@/components/issue/issue-icons'
import { DocumentMenuItems } from '@/components/documents/document-menu'
import type { DocumentActionContext, DocumentUiAction } from '@/components/documents/document-actions'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { useOptionalRouteNavigation } from '@/hooks/use-optional-route-navigation'
import { LinearDropdownMenuContent } from '@/components/ui/row-context-menu'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, FlowDocument, ProjectResource, Team } from '@/types/flow'
import { ProjectMenuItem, ProjectSubmenu } from './project-menu-primitives'
import { resourceChipTitle } from './project-resource-link-name'

type ResourceUpdate = (id: string, input: { pinnedTeamIds?: string[] }) => Promise<ProjectResource>

/** Absolute URL for a resource (document resources carry an app-relative path). */
function resourceAbsoluteURL(url: string) {
  try { return new URL(url, window.location.origin).href } catch { return url }
}

type Translate = (source: string) => string

const copyText = (t: Translate, value: string, message: string) => void navigator.clipboard.writeText(value).then(() => toast.success(t(message)), () => toast.error(t('Could not copy to clipboard')))

/**
 * The "⋮" menu on a project Resources pill. Links get Linear's short link menu
 * (Copy link, Pin to team, Edit, Delete); documents get the shared document menu
 * (components/documents/document-menu.tsx, `resource` variant).
 */
export function ProjectResourceMenu({ data, document, onDeleteLink, onEditLink, onOpenDocumentHistory, onReload, onUpdate, resource, teams }: {
  data?: BootstrapData
  document?: FlowDocument
  onDeleteLink: () => void
  onEditLink: () => void
  onOpenDocumentHistory?: (document: FlowDocument) => void
  onReload?: () => Promise<void>
  onUpdate: ResourceUpdate
  resource: ProjectResource
  teams: Team[]
}) {
  const { t } = useI18n()
  const navigate = useOptionalRouteNavigation()
  const ctx = useMemo<DocumentActionContext | undefined>(() => data ? { data, reload: () => onReload?.() ?? Promise.resolve(), navigate, t } : undefined, [data, navigate, onReload, t])
  const title = resourceChipTitle(resource, document, t)
  const onUiAction = document && onOpenDocumentHistory ? (action: DocumentUiAction) => { if (action === 'history') onOpenDocumentHistory(document) } : undefined
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild><button aria-label={`${title} actions`} data-i18n-ignore type="button"><MoreVertical size={14}/></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      {document && ctx
        ? <LinearDropdownMenuContent align="end" className="project-resource-action-menu" label={t('Document actions')}>
          <DocumentMenuItems ctx={ctx} document={document} onUiAction={onUiAction} variant="resource"/>
        </LinearDropdownMenuContent>
        : <DropdownMenu.Content data-flow-motion="floating" align="end" className="project-action-menu project-resource-action-menu" data-kind={document ? 'document' : 'link'} collisionPadding={16} sideOffset={4}>
          <ProjectMenuItem icon={null} label="Copy link" onSelect={() => copyText(t, resourceAbsoluteURL(resource.url), 'Resource link copied')}/>
          {!document && <>
            <PinToTeam onUpdate={onUpdate} resource={resource} teams={teams}/>
            <ProjectMenuItem icon={null} label="Edit" onSelect={onEditLink}/>
            <ProjectMenuItem icon={null} label="Delete" onSelect={onDeleteLink}/>
          </>}
        </DropdownMenu.Content>}
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function PinToTeam({ onUpdate, resource, teams }: { onUpdate: ResourceUpdate; resource: ProjectResource; teams: Team[] }) {
  const { t } = useI18n()
  const pinned = resource.pinnedTeamIds ?? []
  return <ProjectSubmenu label="Pin to team" icon={null} className="project-resource-submenu">
    {teams.map(team => {
      const checked = pinned.includes(team.id)
      return <DropdownMenu.CheckboxItem checked={checked} key={team.id} onCheckedChange={() => void onUpdate(resource.id, { pinnedTeamIds: checked ? pinned.filter(id => id !== team.id) : [...pinned, team.id] })}>
        <span className="project-menu-checkbox project-resource-menu__check" data-checked={checked || undefined}>{checked && <CheckboxMark/>}</span>
        <TeamRow team={team}/>
      </DropdownMenu.CheckboxItem>
    })}
    {!teams.length && <div className="project-action-menu__empty">{t('No teams')}</div>}
  </ProjectSubmenu>
}

function TeamRow({ showKey = true, team }: { showKey?: boolean; team: Team }) {
  return <><span className="project-menu-icon project-resource-menu__team-icon"><TeamIcon team={team} size={14}/></span><span className="project-menu-label project-resource-menu__team" data-i18n-ignore>{team.name}{showKey && <small>{team.key}</small>}</span></>
}
