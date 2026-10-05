import { Fragment, type ReactNode } from 'react'
import type { BootstrapData, PulseItem, PulseReason } from '@/types/flow'

type Translate = (source: string) => string

/** Replaces {name} placeholders with React nodes (names render emphasized). */
export function fillTemplate(template: string, values: Record<string, ReactNode>): ReactNode[] {
  return template.split(/(\{\w+\})/).filter(Boolean).map((part, index) => {
    const key = part.match(/^\{(\w+)\}$/)?.[1]
    return <Fragment key={index}>{key && key in values ? values[key] : part}</Fragment>
  })
}

/** Joins names the way Linear does: "A", "A and B", "A, B and C". */
export function joinNames(names: string[], t: Translate) {
  if (names.length <= 1) return names[0] ?? ''
  return `${names.slice(0, -1).join(', ')} ${t('and')} ${names[names.length - 1]}`
}

export type PulseReasonCopy = { text: ReactNode; footer?: string; plain: string }

function reasonNames(reason: PulseReason, data: Pick<BootstrapData, 'projects' | 'initiatives' | 'teams'>, kind: 'project' | 'initiative' | 'team') {
  const names = reason.names?.length ? reason.names : reason.sourceNames
  if (names?.length) return names
  const ids = reason.sourceIds ?? []
  const pool: { id: string; name: string }[] = kind === 'project' ? data.projects : kind === 'initiative' ? data.initiatives : data.teams
  return ids.map(id => pool.find(entry => entry.id === id)?.name).filter((name): name is string => Boolean(name))
}

const FOOTER_PROJECT_AND_INITIATIVE = 'Modify your Pulse subscriptions from an initiative or project page using the subscription menu in the top-right corner.'
const FOOTER_TEAM = 'Modify your Pulse subscriptions from a team’s menu in the sidebar.'

/** Linear's "Why am I seeing this?" copy for one reason. */
export function pulseReasonCopy(item: Pick<PulseItem, 'kind' | 'source'>, reason: PulseReason, data: Pick<BootstrapData, 'projects' | 'initiatives' | 'teams'>, t: Translate): PulseReasonCopy {
  const strong = (name: string) => <strong data-i18n-ignore>{name}</strong>
  const build = (template: string, name: string, footer?: string): PulseReasonCopy => ({
    text: fillTemplate(t(template), { name: strong(name) }),
    plain: t(template).replace('{name}', name),
    footer,
  })
  const here = item.kind === 'project' ? t('this project') : t('this initiative')
  switch (reason.type) {
    case 'mentioned': return { text: t('You’re mentioned in this update.'), plain: t('You’re mentioned in this update.') }
    case 'author': return { text: t('You’re the author of this update.'), plain: t('You’re the author of this update.') }
    case 'initiativeOwner': {
      if (item.kind === 'initiative') return build('As an owner of {name}, you’re automatically subscribed to updates on this initiative.', item.source.name || here, FOOTER_PROJECT_AND_INITIATIVE)
      const names = reasonNames(reason, data, 'initiative')
      return build('As an owner of {name}, you’re automatically subscribed to updates on this project.', names.length ? joinNames(names, t) : t('an initiative this project belongs to'), FOOTER_PROJECT_AND_INITIATIVE)
    }
    case 'initiativeProjectMember': {
      const names = item.kind === 'initiative' ? [item.source.name] : reasonNames(reason, data, 'initiative')
      return build('As a member of a project belonging to {name}, you’re automatically subscribed to updates on this initiative.', joinNames(names, t) || t('this initiative'), FOOTER_PROJECT_AND_INITIATIVE)
    }
    case 'projectMember': return build('As a member of {name}, you’re automatically subscribed to updates on this project.', item.source.name || here, FOOTER_PROJECT_AND_INITIATIVE)
    case 'subscribed': return build('You’re subscribed to updates on {name}.', item.source.name || here, FOOTER_PROJECT_AND_INITIATIVE)
    case 'teamProjectUpdates': {
      const names = reasonNames(reason, data, 'team')
      const initiatives = reason.initiativeNames?.length ? reason.initiativeNames : (reason.initiativeIds ?? []).map(id => data.initiatives.find(entry => entry.id === id)?.name).filter((name): name is string => Boolean(name))
      if (initiatives.length) {
        const parts = [names.length ? (names.length === 1 ? t('the {names} team') : t('the {names} teams')).replace('{names}', joinNames(names, t)) : '', (initiatives.length === 1 ? t('the {names} initiative') : t('the {names} initiatives')).replace('{names}', joinNames(initiatives, t))].filter(Boolean)
        return build('You’re subscribed to project updates for {name}.', parts.join(` ${t('and')} `), parts.length > 1 || !names.length ? FOOTER_PROJECT_AND_INITIATIVE : FOOTER_TEAM)
      }
      if (!names.length) return { text: t('You’re subscribed to project updates for a team or initiative this project belongs to.'), plain: t('You’re subscribed to project updates for a team or initiative this project belongs to.'), footer: FOOTER_TEAM }
      const label = (names.length === 1 ? t('the {names} team') : t('the {names} teams')).replace('{names}', joinNames(names, t))
      return build('You’re subscribed to project updates for {name}.', label, FOOTER_TEAM)
    }
    default: return { text: t('No reason to display.'), plain: t('No reason to display.') }
  }
}

/** Linear shows one reason; mentions and authorship win over subscription rules. */
export function primaryPulseReason(reasons: PulseReason[]) {
  return reasons.find(reason => reason.type === 'mentioned') ?? reasons.find(reason => reason.type === 'author') ?? reasons[0]
}
