import type { AgentApproval, AgentApprovalTarget } from '@/types/flow'

type Translate = (source: string) => string
type Choice = { label: string; value: string }

const HEALTH_LABELS: Record<string, string> = { onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track' }

/** A linked-item kind the server counts ("issues", "milestones", …), under its own "affected:" key. */
function affectedNoun(kind: string, t: Translate) {
  const key = `affected:${kind}`
  const translated = t(key)
  return translated === key ? kind.replace(/([a-z])([A-Z])/g, '$1 $2').toLowerCase() : translated
}

function escapeLabel(value: string) {
  return value.replace(/[[\]\\]/g, match => `\\${match}`)
}

function targetLink(target: AgentApprovalTarget) {
  const label = target.title ? `${target.label} ${target.title}` : target.label
  return target.href ? `[${escapeLabel(label)}](${target.href})` : label
}

/**
 * The approval as markdown in the viewer's language: the reference app asks before a bulk delete (every target as a chip,
 * sub-issues flagged) and before posting an update it wrote (the full text as a quote).
 */
export function agentApprovalMarkdown(approval: AgentApproval, t: Translate) {
  const sections: string[] = []
  const targets = approval.targets ?? []
  if (targets.length) {
    const lines = targets.map(target => {
      const notes = [
        ...(target.subIssues ? [t('{count} sub-issues').replace('{count}', String(target.subIssues))] : []),
        ...Object.entries(target.affected ?? {}).filter(([, count]) => count > 0).map(([kind, count]) => `${count} ${affectedNoun(kind, t)}`),
      ]
      return `- ${targetLink(target)}${target.permanent ? ` (${t('permanent')})` : ''}${notes.length ? ` (${t('also affects')} ${notes.join(', ')})` : ''}`
    })
    // Customers, loops and views have no trash: say so when any target is one of them.
    const heading = targets.some(target => target.permanent)
      ? t('Delete these {count} items? Items marked permanent can’t be restored.')
      : t('Delete these {count} items? They go to the trash and can be restored.')
    sections.push(`${heading.replace('{count}', String(targets.length))}\n\n${lines.join('\n')}`)
  }
  if (approval.kind === 'publish' || approval.body) {
    const heading = approval.parent ? t('Post this update to {name}?').replace('{name}', targetLink(approval.parent)) : t('Post this update?')
    const quote = (approval.body ?? '').split('\n').map(line => `> ${line}`).join('\n')
    const health = approval.health ? `\n\n${t('Health')}: ${t(HEALTH_LABELS[approval.health] ?? approval.health)}` : ''
    sections.push(`${heading}\n\n${quote}${health}`)
  }
  if (!sections.length && approval.kind === 'connector') sections.push(t('Allow this connector action?'))
  return sections.join('\n\n')
}

/** Approve / Reject pills, translated (option values stay "approve" / "reject"). */
export function agentApprovalChoices(options: Choice[], t: Translate): Choice[] {
  return options.map(option => ({ ...option, label: option.value === 'approve' ? t('Approve') : option.value === 'reject' ? t('Reject') : option.label }))
}
