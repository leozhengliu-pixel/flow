import { useRef, useState } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Link2, Plus } from 'lucide-react'
import { FlowOptionsIcon } from '@/components/issue/flow-header-icons'
import { CalendarIcon, LabelIcon, MembersIcon, SlackIcon } from '@/components/issue/issue-icons'
import { Avatar } from '@/components/issue/issue-row'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import { ProjectLabelMenuContent } from '@/components/property/project-label-menu-content'
import { projectLabelOptions } from '@/components/property/project-label-menu-model'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { toggleGroupedLabelIds } from '@/lib/labels'
import { useI18n } from '@/i18n/i18n'
import type { Initiative } from '@/types/flow'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { ProjectDetailProps } from './project-detail-types'
import { DependencyProjectPicker } from './project-details-sidebar'
import { initiativeStatusLabel, inviteProjectMember } from './project-detail-helpers'
import { ProjectMenuItem, ProjectMenuSearch, ProjectSubmenu } from './project-menu-primitives'
import { ProjectSlackDialog } from './project-slack-dialog'

function initiativePropertyOptions(initiatives: Initiative[], selectedIds: string[]): PropertyOption[] {
  return [...initiatives].sort((a,b) => Number(selectedIds.includes(b.id)) - Number(selectedIds.includes(a.id)) || a.name.localeCompare(b.name)).map(item => ({
    id:item.id, label:item.name, icon:<ViewGlyph icon={item.icon || 'Initiative'} color={item.color}/>,
    groupLabel:selectedIds.includes(item.id) ? 'Selected' : initiativeStatusLabel(item.status), i18nIgnore:true,
  }))
}

type Props = Pick<ProjectDetailProps, 'initiatives'|'labels'|'labelGroups'|'project'|'projectRelations'|'projects'|'users'|'viewer'|'onCreateLabel'> & {
  integrationConnections?: ProjectDetailProps['integrationConnections']
  save: (input: ProjectMutationInput) => Promise<void>
  onUpdateProject: ProjectDetailProps['onUpdate']
}

export function ProjectPropertiesMenu({ initiatives, labels, labelGroups, project, projectRelations, projects, users, viewer, save, onUpdateProject, onCreateLabel, integrationConnections = [] }: Props) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [startOpen, setStartOpen] = useState(false)
  const [query,setQuery] = useState('')
  const [slackOpen,setSlackOpen] = useState(false)
  const visible = (label:string) => !query || `${label} ${t(label)}`.toLocaleLowerCase().includes(query.toLocaleLowerCase())
  const anchor = useRef<HTMLButtonElement>(null)
  const labelIds = (project.labelIds ?? []).filter(id => labels.some(label => label.id === id))
  const memberIds = [...new Set([...(project.memberIds ?? []), ...(project.lead ? [project.lead.id] : [])])]
  const memberOptions: PropertyOption[] = [
    ...users.map(user => ({id:user.id,label:user.displayName,person:user,icon:<Avatar name={user.displayName}/>,groupLabel:memberIds.includes(user.id) ? 'Project members' : 'Users from the project team',end:project.lead?.id === user.id ? 'Project lead' : user.active ? undefined : 'Invited',i18nIgnore:true})),
    {id:'__invite__',label:'Invite and add…',icon:<Plus size={16}/>,groupLabel:'New user'},
  ]
  const updateMember = (id: string) => {
    if (id === '__invite__') { setOpen(false); inviteProjectMember(); return }
    const nextIds = toggle(memberIds, id)
    void save({memberIds:nextIds,...(id === project.lead?.id && !nextIds.includes(id) ? {leadId:''} : {})})
  }
  return <>
    <DropdownMenu.Root open={open} onOpenChange={next => { setOpen(next); if (!next) setQuery('') }}>
      <DropdownMenu.Trigger asChild><button ref={anchor} aria-label={t('More project properties')} className="project-overview__more" type="button"><FlowOptionsIcon/></button></DropdownMenu.Trigger>
      <DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className="project-action-menu project-action-menu--properties" sideOffset={4} collisionPadding={16} onCloseAutoFocus={event => { if (startOpen) event.preventDefault() }}>
        <ProjectMenuSearch label="Filter project properties" query={query} onChange={setQuery}/>
        {visible('Members') && !memberIds.some(id => id !== project.lead?.id) && <ProjectSubmenu label="Members" icon={<MembersIcon size={16}/>} shortcut="P then M" searchable className="property-command-surface property-command-standard project-property-submenu is-members">{close => <PropertyMenu embedded multiple label="Members" options={memberOptions} selectedIds={memberIds} searchPlaceholder="Change members…" searchShortcut="P, then M" onChange={updateMember} onOpenChange={next => { if (!next) close() }}/>}</ProjectSubmenu>}
        {visible('Initiatives') && initiatives.length > 0 && <ProjectSubmenu label="Initiatives" icon={<ViewGlyph icon="Initiative" color="currentColor"/>} shortcut="P then N" alignOffset={-30.5} className="property-command-surface property-command-standard project-property-submenu is-initiatives">{close => <PropertyMenu embedded hideSearch multiple label="Initiatives" options={initiativePropertyOptions(initiatives, project.initiatives ?? [])} selectedIds={project.initiatives ?? []} searchPlaceholder="Change initiatives…" searchShortcut="P, then N" onChange={id => void save({initiatives:toggle(project.initiatives ?? [],id)})} onOpenChange={next => { if (!next) close() }}/>}</ProjectSubmenu>}
        {visible('Start date…') && !project.startDate && <ProjectMenuItem label="Start date…" shortcut="⌃ ⌥ S" icon={<CalendarIcon variant="start" size={16}/>} onSelect={() => setStartOpen(true)}/>}
        {visible('Dependencies') && <ProjectSubmenu label="Dependencies" icon={<Link2 size={16}/>}>
          <ProjectSubmenu label="Blocked by" icon={<Link2 size={16}/>} alignOffset={-30.5} className="project-dependency-projects"><DependencyProjectPicker direction="blockedBy" onUpdate={save} onUpdateProject={onUpdateProject} project={project} projectRelations={projectRelations} projects={projects} viewer={viewer}/></ProjectSubmenu>
          <ProjectSubmenu label="Blocking" icon={<Link2 size={16}/>} alignOffset={-30.5} className="project-dependency-projects"><DependencyProjectPicker direction="blocking" onUpdate={save} onUpdateProject={onUpdateProject} project={project} projectRelations={projectRelations} projects={projects} viewer={viewer}/></ProjectSubmenu>
        </ProjectSubmenu>}
        {visible('Labels') && !labelIds.length && <ProjectSubmenu label="Labels" icon={<LabelIcon size={16}/>} shortcut="P then L" searchable className="property-command-surface property-command-project-labels project-property-submenu"><ProjectLabelMenuContent options={projectLabelOptions(labels,labelGroups)} selectedIds={labelIds} onChoose={id => void save({labelIds:toggleGroupedLabelIds(labelIds,id,labels)})} onCreate={onCreateLabel ? async (name,groupId) => { const created = await onCreateLabel(name,groupId); await save({labelIds:toggleGroupedLabelIds(labelIds,created.id,[...labels,created])}) } : undefined} onClose={() => setOpen(false)}/></ProjectSubmenu>}
        {!project.slackChannelId && visible('Connect existing Slack channel…') && <ProjectMenuItem label="Connect existing Slack channel…" icon={<SlackIcon size={16}/>} onSelect={()=>setSlackOpen(true)}/>}
      </DropdownMenu.Content></DropdownMenu.Portal>
    </DropdownMenu.Root>
    <ProjectDatePicker externalAnchor={anchor} open={startOpen} onOpenChange={setStartOpen} label="Start date" value={project.startDate} max={project.targetDate} resolution={project.startDateResolution} align="start" onChange={(startDate,startDateResolution) => void save({startDate,startDateResolution:startDateResolution ?? ''})}>{null}</ProjectDatePicker>
    <ProjectSlackDialog open={slackOpen} onOpenChange={setSlackOpen} project={project} connections={integrationConnections} onSave={save}/>
  </>
}

function toggle(values: string[], value: string) { return values.includes(value) ? values.filter(id => id !== value) : [...values,value] }
