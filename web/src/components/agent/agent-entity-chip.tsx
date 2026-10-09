import { useContext, type ReactNode } from 'react'
import { MessageSquare } from 'lucide-react'
import { NodeViewWrapper, type ReactNodeViewProps } from '@tiptap/react'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { CustomerDefaultLogoIcon } from '@/components/customer/customer-logo'
import { CycleIcon, ProjectIcon, ReviewStatusValueGlyph, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { MilestoneProgressIcon } from '@/components/issue/milestone-progress-icon'
import { HealthGlyph } from '@/components/project-detail/health-glyph'
import { ReleaseStatusIcon } from '@/components/releases/release-icons'
import { AppLink } from '@/components/ui/app-link'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { AgentEntityDataContext } from './agent-entity-data'
import { useAgentEntity } from './agent-entity-fetch'
import { AgentEntityHover } from './agent-entity-hover'
import { agentEntityPath, isAgentTextMention, type AgentEntity, type AgentEntityKind } from './agent-entity-refs'
import styles from './agent-answer.module.css'

function linkGlyph(icon: 'update' | 'comment' | 'milestone') {
  if (icon === 'comment') return <MessageSquare size={15}/>
  if (icon === 'milestone') return <MilestoneProgressIcon size={15}/>
  return <HealthGlyph className={styles.updateGlyph} health="noUpdate"/>
}

export type ChipParts = { icon?: ReactNode; identifier?: string; label: string; suffix?: string }

export function chipParts(entity: AgentEntity, t: (source: string) => string): ChipParts {
  switch (entity.kind) {
    case 'issue': return { icon: <StatusIcon size={15} state={entity.issue.state}/>, identifier: entity.issue.identifier, label: entity.issue.title }
    case 'project': return { icon: entity.project.icon && entity.project.icon !== 'Project' ? <ViewGlyph color={entity.project.color} icon={entity.project.icon}/> : <ProjectIcon size={15} style={{ color: entity.project.color }}/>, label: entity.project.name }
    case 'initiative': return { icon: <ViewGlyph color={entity.initiative.color} icon={entity.initiative.icon || 'Initiative'}/>, label: entity.initiative.name }
    case 'document': return { icon: <DocumentGlyph document={entity.document}/>, label: entity.document.title }
    case 'user': return { label: `@${entity.user.displayName || entity.user.name}` }
    case 'team': return { icon: <TeamIcon size={15} team={entity.team}/>, label: entity.team.name }
    case 'cycle': return { icon: <CycleIcon cycle={entity.cycle} size={15}/>, label: entity.cycle.name || `${t('Cycle')} ${entity.cycle.number}` }
    case 'label': return { icon: <span aria-hidden="true" className={styles.labelDot} style={{ background: entity.label.color }}/>, label: entity.label.name }
    case 'milestone': return { icon: <MilestoneProgressIcon size={15}/>, label: entity.milestone.name, suffix: entity.project.name }
    case 'customer': return { icon: <CustomerDefaultLogoIcon size={15}/>, label: entity.customer.name }
    case 'release': return { icon: <ReleaseStatusIcon size={15} status={entity.release.status}/>, label: entity.release.name }
    case 'view': return { icon: <ViewGlyph color={entity.view.color} icon={entity.view.icon}/>, label: entity.view.name }
    case 'review': return { icon: <ReviewStatusValueGlyph size={15} status={entity.review.status}/>, label: entity.review.title }
    case 'link': return { icon: linkGlyph(entity.icon), label: entity.label }
  }
}

/**
 * Linear's inline mention: a tinted chip with the resource's own icon, a secondary identifier and a primary title.
 * Hovering or focusing it opens the resource's card; clicking navigates (cmd/ctrl/middle-click opens a new tab).
 */
export function AgentEntityChip({ data, entity }: { data: BootstrapData; entity: AgentEntity }) {
  const { t } = useI18n()
  const parts = chipParts(entity, t)
  const textMention = entity.kind !== 'link' && isAgentTextMention(entity.kind)
  return (
    <AgentEntityHover data={data} entity={entity}>
      <AppLink className={textMention ? `${styles.entityChip} ${styles.entityMention}` : styles.entityChip} contentEditable={false} data-agent-entity={entity.kind === 'link' ? entity.icon : entity.kind} data-i18n-ignore href={agentEntityPath(data, entity)}>
        {parts.icon && <span className={styles.entityLead}>{parts.icon}{' '}</span>}
        {parts.identifier && <><span className={styles.entityIdentifier}>{parts.identifier}</span>{' '}</>}
        <span className={styles.entityTitle}>{parts.label}</span>
        {parts.suffix && <span className={styles.entityIdentifier}>{' · '}{parts.suffix}</span>}
      </AppLink>
    </AgentEntityHover>
  )
}

/** Tiptap node view for AgentEntityNode; entities that cannot be found fall back to their plain label. */
export function AgentEntityChipView({ node }: ReactNodeViewProps) {
  const data = useContext(AgentEntityDataContext)
  const kind = String(node.attrs.kind) as AgentEntityKind
  const label = String(node.attrs.label ?? '')
  const state = useAgentEntity(data, { kind, id: String(node.attrs.id), label, href: node.attrs.href ? String(node.attrs.href) : undefined })
  return (
    <NodeViewWrapper as="span" className={styles.entityWrap}>
      {data && state.status === 'ready' ? <AgentEntityChip data={data} entity={state.entity}/> : <span className={state.status === 'loading' ? styles.entityPending : undefined}>{label}</span>}
    </NodeViewWrapper>
  )
}
