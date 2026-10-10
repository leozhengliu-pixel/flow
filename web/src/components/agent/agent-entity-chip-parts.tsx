import type { ReactNode } from 'react'
import { MessageSquare } from 'lucide-react'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { CustomerDefaultLogoIcon } from '@/components/customer/customer-logo'
import { CycleIcon, ProjectIcon, ReviewStatusValueGlyph, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { MilestoneProgressIcon } from '@/components/issue/milestone-progress-icon'
import { HealthGlyph } from '@/components/project-detail/health-glyph'
import { ReleaseStatusIcon } from '@/components/releases/release-icons'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import type { AgentEntity } from './agent-entity-refs'
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
    case 'document': return { icon: <DocumentGlyph document={entity.document}/>, label: entity.document.title.trim() || t('Untitled') }
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
