import * as Tooltip from '@radix-ui/react-tooltip'
import { MessageSquare } from 'lucide-react'
import type { ReactElement, ReactNode } from 'react'
import { documentParent } from '@/components/documents/document-actions'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { CustomerDefaultLogoIcon } from '@/components/customer/customer-logo'
import { CalendarIcon, CycleIcon, NoAssigneeIcon, PriorityIcon, ProjectIcon, ProjectStatusIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { MilestoneProgressIcon } from '@/components/issue/milestone-progress-icon'
import { HealthGlyph } from '@/components/project-detail/health-glyph'
import { healthColor } from '@/components/project-detail/health-color'
import { LabelHoverPreviewContent } from '@/components/property/label-hover-preview'
import { InitiativeStatusIcon } from '@/components/initiatives/initiative-shared'
import { ReleaseStatusIcon } from '@/components/releases/release-icons'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { markdownPlainText } from '@/lib/markdown-plain-text'
import type { BootstrapData, FlowDocument, Initiative, Issue, Project, SavedView } from '@/types/flow'
import type { AgentEntity } from './agent-entity-refs'
import styles from './agent-entity-hover.module.css'

/** Linear opens an entity chip's card after a short hover and closes it the moment the pointer leaves. */
const HOVER_DELAY_MS = 500

/** Wraps a chip (or list row) so hovering or focusing it opens the entity's card. */
export function AgentEntityHover({ children, data, entity, side = 'top' }: { children: ReactElement; data: BootstrapData; entity: AgentEntity; side?: 'top' | 'left' | 'right' | 'bottom' }) {
  return (
    <Tooltip.Provider delayDuration={HOVER_DELAY_MS} skipDelayDuration={0}>
      <Tooltip.Root disableHoverableContent>
        <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content align="start" className={styles.card} collisionPadding={8} data-agent-entity-card={entity.kind === 'link' ? entity.icon : entity.kind} data-flow-motion="tooltip" side={side} sideOffset={3}>
            <AgentEntityCardBody data={data} entity={entity}/>
          </Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
  )
}

function Prop({ children, icon }: { children: ReactNode; icon?: ReactNode }) {
  return <span className={styles.prop}>{icon}<span>{children}</span></span>
}

function Person({ user }: { user?: { avatarUrl?: string; displayName?: string; name: string } }) {
  const { t } = useI18n()
  if (!user) return <Prop icon={<NoAssigneeIcon className={styles.dim} size={16}/>}>{t('Unassigned')}</Prop>
  const name = user.displayName || user.name
  return <span className={styles.prop}><UserAvatar avatarUrl={user.avatarUrl} className={styles.avatar} name={name}/><span data-i18n-ignore>{name}</span></span>
}

function Progress({ percent }: { percent: number }) {
  return <span aria-hidden="true" className={styles.ring} style={{ '--progress': `${Math.max(0, Math.min(100, percent))}%` } as React.CSSProperties}/>
}

function useDateFormat() {
  const { formatDate } = useI18n()
  return (value?: string) => {
    if (!value) return ''
    const date = new Date(/^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T00:00:00` : value)
    return Number.isNaN(date.getTime()) ? '' : formatDate(date, { month: 'short', day: 'numeric' })
  }
}

function fill(template: string, values: Record<string, string | number>) {
  return Object.entries(values).reduce((text, [key, value]) => text.replace(`{${key}}`, String(value)), template)
}

function healthText(health: Project['health'], t: (source: string) => string) {
  return health === 'onTrack' ? t('On track') : health === 'atRisk' ? t('At risk') : health === 'offTrack' ? t('Off track') : t('No update')
}

function IssueCard({ issue }: { issue: Issue }) {
  const { t } = useI18n()
  return <>
    <div className={styles.identifier} data-i18n-ignore>{issue.identifier}</div>
    <div className={styles.title}><span data-i18n-ignore>{issue.title}</span></div>
    <hr className={styles.rule}/>
    <div className={styles.props}>
      <Prop icon={<StatusIcon size={16} state={issue.state}/>}>{t(issue.state.name)}</Prop>
      <Prop icon={<PriorityIcon priority={issue.priority} size={16}/>}>{t(issue.priorityLabel)}</Prop>
      {issue.assignee && <Person user={issue.assignee}/>}
    </div>
  </>
}

function ProjectCard({ data, project }: { data: BootstrapData; project: Project }) {
  const { t } = useI18n()
  const team = (data.teams ?? []).find(item => (project.teamIds ?? []).includes(item.id))
  const milestone = (project.milestones ?? [])[0]
  const percent = Math.round((project.progress ?? 0) * 100)
  return <>
    <div className={styles.title}><ProjectGlyph project={project}/><span data-i18n-ignore>{project.name}</span></div>
    {(project.summary || project.description) && <p className={styles.summary} data-i18n-ignore>{markdownPlainText(project.summary || project.description)}</p>}
    <hr className={styles.rule}/>
    <div className={styles.props}>
      <Prop icon={<ProjectStatusIcon color={project.status.color} name={project.status.name} progress={project.progress} size={16} type={project.status.type}/>}>{t(project.status.name)}</Prop>
      {milestone && <Prop icon={<MilestoneProgressIcon size={16}/>}><span data-i18n-ignore>{milestone.name}</span></Prop>}
      {team && <Prop icon={<TeamIcon size={16} team={team}/>}><span data-i18n-ignore>{team.name}</span></Prop>}
      <Prop icon={<PriorityIcon priority={project.priority} size={16}/>}>{t(project.priorityLabel)}</Prop>
    </div>
    <div className={`${styles.props} ${styles.secondRow}`}>
      <Prop icon={<Progress percent={percent}/>}>{fill(t('{percent}% of {count} issues'), { percent, count: project.issueCount ?? 0 })}</Prop>
    </div>
  </>
}

function ProjectGlyph({ project }: { project: Pick<Project, 'color' | 'icon'> }) {
  return project.icon && project.icon !== 'Project'
    ? <ViewGlyph color={project.color} icon={project.icon}/>
    : <ProjectIcon size={16} style={{ color: project.color }}/>
}

function InitiativeCard({ data, initiative }: { data: BootstrapData; initiative: Initiative }) {
  const { formatDate, t } = useI18n()
  const latest = (data.initiativeUpdates?.[initiative.id] ?? [])[0]
  const status = initiative.status.charAt(0).toUpperCase() + initiative.status.slice(1)
  return <>
    <div className={styles.titleRow}>
      <div className={styles.title}><ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'}/><span data-i18n-ignore>{initiative.name}</span></div>
      {initiative.health !== 'noUpdate' && <span className={styles.health} style={{ color: healthColor(initiative.health) }}><HealthGlyph className={styles.healthGlyph} health={initiative.health}/>{healthText(initiative.health, t)}{latest ? ` · ${formatDate(latest.createdAt, { month: 'short', day: 'numeric' })}` : ''}</span>}
    </div>
    {(initiative.summary || initiative.description) && <p className={styles.summary} data-i18n-ignore>{markdownPlainText(initiative.summary || initiative.description)}</p>}
    <hr className={styles.rule}/>
    <div className={styles.props}>
      <Prop icon={<InitiativeStatusIcon status={initiative.status}/>}>{t(status)}</Prop>
      <Prop icon={<PriorityIcon priority={initiative.priority} size={16}/>}>{t(initiative.priorityLabel)}</Prop>
      {initiative.owner && <Person user={initiative.owner}/>}
    </div>
  </>
}

function DocumentCard({ data, document }: { data: BootstrapData; document: FlowDocument }) {
  const { t } = useI18n()
  const date = useDateFormat()
  const parent = documentParent(data, document)
  const issue = parent?.type === 'issue' ? (data.issues ?? []).find(item => item.id === parent.id) : undefined
  const summary = documentSummary(document)
  const revisions = document.revisions ?? []
  const lastEditor = revisions.length ? revisions.reduce((latest, revision) => Date.parse(revision.createdAt) > Date.parse(latest.createdAt) ? revision : latest).author : document.creator
  const editor = lastEditor?.displayName || lastEditor?.name
  return <>
    <div className={styles.title}><DocumentGlyph document={document}/><span data-i18n-ignore>{document.title.trim() || t('Untitled')}</span></div>
    {summary && <p className={styles.summary} data-i18n-ignore>{summary}</p>}
    <hr className={styles.rule}/>
    <div className={styles.column}>
      {parent?.type === 'project' && <Prop icon={<ProjectGlyph project={parent.project}/>}><span data-i18n-ignore>{parent.project.name}</span></Prop>}
      {parent?.type === 'team' && <Prop icon={<TeamIcon size={16} team={parent.team}/>}><span data-i18n-ignore>{parent.team.name}</span></Prop>}
      {parent?.type === 'initiative' && <Prop icon={<ViewGlyph color={parent.initiative.color} icon={parent.initiative.icon || 'Initiative'}/>}><span data-i18n-ignore>{parent.initiative.name}</span></Prop>}
      {issue && <Prop icon={<StatusIcon size={16} state={issue.state}/>}><span data-i18n-ignore>{issue.identifier} {issue.title}</span></Prop>}
      <Prop icon={<CalendarIcon className={styles.dim} size={16} variant="start"/>}>{editor ? fill(t('Last edited {date} by {name}'), { date: date(document.updatedAt), name: editor }) : fill(t('Last edited {date}'), { date: date(document.updatedAt) })}</Prop>
    </div>
  </>
}

/** The body as one line of plain text (a leading heading that repeats the title is dropped), for the card's summary line. */
function documentSummary(document: Pick<FlowDocument, 'title' | 'content'>) {
  const body = (document.content ?? '').replace(/^\s*#{1,6}[ \t]+(.*)\n+/, (match, heading: string) => heading.trim() === document.title.trim() ? '' : match)
  const line = markdownPlainText(body).trim()
  return line.length > 160 ? `${line.slice(0, 157)}…` : line
}

function ViewCard({ data, view }: { data: BootstrapData; view: SavedView }) {
  const { t } = useI18n()
  const date = useDateFormat()
  const owner = (data.users ?? []).find(user => user.id === view.ownerId)
  return <>
    <div className={styles.title}><ViewGlyph color={view.color} icon={view.icon}/><span data-i18n-ignore>{view.name}</span></div>
    {view.description && <p className={styles.summary} data-i18n-ignore>{markdownPlainText(view.description)}</p>}
    <hr className={styles.rule}/>
    <div className={styles.props}>
      {owner && <Person user={owner}/>}
      <Prop icon={<CalendarIcon className={styles.dim} size={16} variant="start"/>}>{fill(t('Last updated {date}'), { date: date(view.updatedAt) })}</Prop>
    </div>
  </>
}

/** The card for one entity: the same details Linear's mention popover lists, drawn from workspace data. */
export function AgentEntityCardBody({ data, entity }: { data: BootstrapData; entity: AgentEntity }) {
  const { t } = useI18n()
  const date = useDateFormat()
  switch (entity.kind) {
    case 'issue': return <IssueCard issue={entity.issue}/>
    case 'project': return <ProjectCard data={data} project={entity.project}/>
    case 'initiative': return <InitiativeCard data={data} initiative={entity.initiative}/>
    case 'document': return <DocumentCard data={data} document={entity.document}/>
    case 'view': return <ViewCard data={data} view={entity.view}/>
    case 'user': {
      const { user } = entity
      const name = user.displayName || user.name
      const teamIds = new Set((data.teamMembers ?? []).filter(member => member.userId === user.id).map(member => member.teamId))
      const teams = (data.teams ?? []).filter(team => teamIds.has(team.id))
      return <>
        <div className={styles.person}>
          <UserAvatar avatarUrl={user.avatarUrl} className={`${styles.avatar} ${styles.avatarLarge}`} name={name}/>
          <div><strong data-i18n-ignore>{name}</strong><span data-i18n-ignore>{user.username ? `@${user.username}` : user.email}</span></div>
        </div>
        {(user.jobTitle || teams.length > 0) && <hr className={styles.rule}/>}
        <div className={styles.column}>
          {user.jobTitle && <Prop><span data-i18n-ignore>{user.jobTitle}</span></Prop>}
          {teams.map(team => <Prop icon={<TeamIcon size={16} team={team}/>} key={team.id}><span data-i18n-ignore>{team.name}</span></Prop>)}
        </div>
      </>
    }
    case 'team': {
      const { team } = entity
      const memberIds = (data.teamMembers ?? []).filter(member => member.teamId === team.id).map(member => member.userId)
      const members = (data.users ?? []).filter(user => memberIds.includes(user.id)).slice(0, 3)
      const project = (data.projects ?? []).find(item => (item.teamIds ?? []).includes(team.id) && !item.archivedAt)
      const document = (data.documents ?? []).find(item => (item.teamIds ?? []).includes(team.id) && !item.archivedAt)
      return <>
        <div className={styles.titleRow}>
          <div className={styles.title}><TeamIcon size={16} team={team}/><span data-i18n-ignore>{team.name} <span className={styles.muted}>({team.key})</span></span></div>
          {members.length > 0 && <span className={styles.facepile}>{members.map(user => <UserAvatar avatarUrl={user.avatarUrl} className={styles.avatar} key={user.id} name={user.displayName || user.name}/>)}</span>}
        </div>
        <hr className={styles.rule}/>
        <div className={styles.column}>
          {project ? <Prop icon={<ProjectGlyph project={project}/>}><span data-i18n-ignore>{project.name}</span></Prop> : <Prop icon={<ProjectIcon size={16}/>}><span className={styles.muted}>{t('No projects')}</span></Prop>}
          {document ? <Prop icon={<DocumentGlyph document={document}/>}><span data-i18n-ignore>{document.title.trim() || t('Untitled')}</span></Prop> : <Prop icon={<DocumentGlyph/>}><span className={styles.muted}>{t('No documents')}</span></Prop>}
        </div>
      </>
    }
    case 'cycle': {
      const { cycle, team } = entity
      return <>
        <div className={styles.title}><CycleIcon cycle={cycle} size={16}/><span data-i18n-ignore>{cycle.name || fill(t('Cycle {number}'), { number: cycle.number })}</span></div>
        <hr className={styles.rule}/>
        <div className={styles.props}>
          <Prop icon={<CalendarIcon className={styles.dim} size={16} variant="start"/>}>{`${date(cycle.startsAt)} – ${date(cycle.endsAt)}`}</Prop>
          {team && <Prop icon={<TeamIcon size={16} team={team}/>}><span data-i18n-ignore>{team.name}</span></Prop>}
          <Prop>{t(cycle.status.charAt(0).toUpperCase() + cycle.status.slice(1))}</Prop>
        </div>
      </>
    }
    case 'label':
      return <div className={styles.embedded}><LabelHoverPreviewContent label={{ name: entity.label.name, color: entity.label.color, description: entity.label.description, issueCount: entity.label.issueCount, scope: entity.label.scope, resourceType: entity.label.resourceType }}/></div>
    case 'milestone': {
      const { milestone, project } = entity
      const issues = data.issueCollectionPaged ? undefined : data.issues.filter(issue => issue.projectMilestoneId === milestone.id)
      const percent = issues?.length ? Math.round(issues.filter(issue => issue.state.type === 'completed').length / issues.length * 100) : 0
      return <>
        <div className={styles.title}><MilestoneProgressIcon progress={percent} size={16}/><span data-i18n-ignore>{milestone.name} <span className={styles.muted}>· {project.name}</span></span></div>
        <hr className={styles.rule}/>
        <div className={styles.props}>
          {issues && <Prop icon={<Progress percent={percent}/>}>{fill(t('{percent}% of {count} issues'), { percent, count: issues.length })}</Prop>}
          {milestone.targetDate && <Prop icon={<CalendarIcon className={styles.dim} size={16}/>}>{date(milestone.targetDate)}</Prop>}
        </div>
      </>
    }
    case 'customer': {
      const { customer } = entity
      return <>
        <div className={styles.title}>{customer.logoUrl ? <img alt="" className={styles.logo} src={customer.logoUrl}/> : <CustomerDefaultLogoIcon size={16}/>}<span data-i18n-ignore>{customer.name}</span></div>
        <hr className={styles.rule}/>
        <div className={styles.props}>
          <Prop><span data-i18n-ignore>{customer.status.charAt(0).toUpperCase() + customer.status.slice(1)}</span></Prop>
          {customer.tier && <Prop><span data-i18n-ignore>{customer.tier}</span></Prop>}
        </div>
      </>
    }
    case 'release': {
      // Linear's release card: "Release · Pipeline", then its stage, date and (continuous pipelines) commit.
      const { release, pipeline } = entity
      const shown = release.releasedAt || release.targetDate
      return <>
        <div className={styles.title}><span data-i18n-ignore>{release.name}{pipeline ? <span className={styles.muted}> · {pipeline.name}</span> : null}</span></div>
        <hr className={styles.rule}/>
        <div className={styles.props}>
          <Prop icon={<ReleaseStatusIcon size={16} status={release.status}/>}><span data-i18n-ignore>{release.stage || t(release.status === 'inProgress' ? 'In progress' : release.status === 'released' ? 'Released' : release.status === 'canceled' ? 'Canceled' : 'Planned')}</span></Prop>
          {shown && <Prop icon={<CalendarIcon className={styles.dim} size={16}/>}>{date(shown)}</Prop>}
          {pipeline?.type === 'continuous' && release.commitSha && <Prop><span data-i18n-ignore>{release.commitSha.slice(0, 7)}</span></Prop>}
        </div>
      </>
    }
    case 'review': {
      const { review } = entity
      return <>
        <div className={styles.identifier} data-i18n-ignore>{`${review.repositoryOwner}/${review.repositoryName}#${review.number}`}</div>
        <div className={styles.title}><span data-i18n-ignore>{review.title}</span></div>
        <hr className={styles.rule}/>
        <div className={styles.props}>
          <Prop>{t(review.status === 'inReview' ? 'In review' : review.status.charAt(0).toUpperCase() + review.status.slice(1))}</Prop>
          <Person user={review.author}/>
        </div>
      </>
    }
    case 'link':
      return <div className={styles.title}>{entity.icon === 'comment' ? <MessageSquare size={16}/> : entity.icon === 'milestone' ? <MilestoneProgressIcon size={16}/> : <HealthGlyph className={styles.healthGlyph} health="noUpdate"/>}<span data-i18n-ignore>{entity.label}</span></div>
  }
}
