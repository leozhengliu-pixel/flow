import { Command } from 'cmdk'
import { Check, Minus, X } from 'lucide-react'
import { Fragment, type ReactNode } from 'react'
import { LabelGroupIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import { labelMenuModel, labelSearchWidth, LABEL_GROUP_SEARCH_MIN, type LabelMenuEntry } from '@/components/property/label-menu-model'
import type { PropertyOption } from '@/components/property/property-menu'
import './label-command-items.css'

/** A label offered by a command menu labels step (the ⌘K "Add labels…" page and the bulk "Change or add labels…"). */
export interface LabelCommandLabel {
  id: string
  name: string
  color?: string
  scope?: string
  groupId?: string
  groupLabel?: string
  groupColor?: string
}

/** Whether all (`true`), some (`'mixed'`) or none of the targeted issues carry a label. */
export type LabelCommandState = boolean | 'mixed'

export { REMOVE_ALL_LABELS } from '@/lib/labels'

/**
 * The labels step of a command menu, laid out like the reference: selected labels and groups first (no heading), then
 * "Team labels" and "Workspace labels". A label group is one row (its labels' state, the selected ones named after it)
 * that opens a step of its own; a search of three characters (two for Chinese) also lists a group's labels as
 * "Group › Label". With `groupId` it renders that group's step: its labels, the current one ticked at the row end.
 */
export function LabelCommandItems({ labels, state, query, groupId, itemClassName, onToggle, onOpenGroup, onRemoveAll }: {
  labels: LabelCommandLabel[]
  state: (labelId: string) => LabelCommandState
  query: string
  groupId?: string
  itemClassName?: string
  onToggle: (labelId: string) => void
  onOpenGroup: (group: { id: string; name: string }) => void
  onRemoveAll?: () => void
}) {
  const { t } = useI18n()
  const rowClass = `label-command-row${itemClassName ? ` ${itemClassName}` : ''}`
  if (groupId) {
    return <Command.Group>{labels.filter(label => label.groupId === groupId).map(label => <Command.Item key={label.id} value={`${label.name} ${label.id}`} className={rowClass} data-label-command="group-label" onSelect={() => onToggle(label.id)}>
      <span className="label-command-icon" aria-hidden="true"><i className="label-command-dot" style={{ background: label.color }}/></span>
      <span className="label-command-copy"><span className="label-command-name" data-i18n-ignore>{label.name}</span></span>
      {state(label.id) === true && <span className="label-command-current" aria-label={t('Current')}><Check size={16}/></span>}
    </Command.Item>)}</Command.Group>
  }
  const options: PropertyOption[] = labels.map(label => ({ id: label.id, label: label.name, color: label.color, scope: label.scope, groupId: label.groupId, groupLabel: label.groupLabel, groupColor: label.groupColor }))
  const selected = new Set(labels.filter(label => state(label.id)).map(label => label.id))
  const model = labelMenuModel(options, selected)
  const searching = labelSearchWidth(query) >= LABEL_GROUP_SEARCH_MIN
  const paths = searching ? options.filter(option => option.groupId && option.groupLabel) : []
  const groupState = (entry: Extract<LabelMenuEntry, { type: 'group' }>): LabelCommandState => entry.group.options.some(option => state(option.id) === true) ? true : entry.group.checked ? 'mixed' : false
  const checkbox = (value: LabelCommandState | undefined) => <span className="label-command-check" aria-hidden="true">{value !== undefined && <span className="label-command-checkbox" data-checked={value === true ? 'true' : value === 'mixed' ? 'mixed' : 'false'}>{value === true ? <Check size={10} strokeWidth={3}/> : value === 'mixed' ? <Minus size={10} strokeWidth={3}/> : null}</span>}</span>
  const row = (entry: LabelMenuEntry): ReactNode => {
    if (entry.type === 'group') {
      const value = groupState(entry)
      return <Command.Item key={`group:${entry.group.id}`} value={`${entry.group.label} group:${entry.group.id}`} className={rowClass} data-label-command="group" aria-checked={value === 'mixed' ? 'mixed' : Boolean(value)} onSelect={() => onOpenGroup({ id: entry.group.id, name: entry.group.label })}>
        {checkbox(value ? value : undefined)}
        <span className="label-command-icon" aria-hidden="true"><LabelGroupIcon color={entry.group.color ?? entry.group.options[0]?.color ?? 'currentColor'} size={16}/></span>
        <span className="label-command-copy"><span className="label-command-name" data-i18n-ignore>{entry.group.label}</span>{entry.group.detail && <span className="label-command-detail" data-i18n-ignore>{entry.group.detail}</span>}</span>
      </Command.Item>
    }
    return labelRow(entry.option)
  }
  const labelRow = (option: PropertyOption, path = false) => <Command.Item key={`${path ? 'path:' : ''}${option.id}`} value={path ? `${option.groupLabel} ${option.label} ${option.id}` : `${option.label} ${option.id}`} className={rowClass} data-label-command={path ? 'path' : 'label'} aria-checked={state(option.id) === 'mixed' ? 'mixed' : Boolean(state(option.id))} onSelect={() => onToggle(option.id)}>
    {checkbox(state(option.id))}
    <span className="label-command-icon" aria-hidden="true"><i className="label-command-dot" style={{ background: option.color }}/></span>
    <span className={`label-command-copy${path ? ' is-path' : ''}`}>{path && <><span className="label-command-path" data-i18n-ignore>{option.groupLabel}</span><span className="label-command-path-chevron" aria-hidden="true"><svg width="5" height="9" viewBox="0 0 5 9"><path d="M1 1.25 3.75 4.5 1 7.75" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.25"/></svg></span></>}<span className="label-command-name" data-i18n-ignore>{option.label}</span></span>
  </Command.Item>
  const sections = [
    { id: 'team', heading: 'Team labels', entries: model.team },
    { id: 'workspace', heading: 'Workspace labels', entries: model.workspace },
  ].filter(section => section.entries.length > 0)
  return <>
    {(onRemoveAll && selected.size > 0 || model.selected.length > 0) && <Command.Group>
      {onRemoveAll && selected.size > 0 && <Command.Item value="Remove all labels" keywords={[t('Remove all labels'), 'clear none']} className={rowClass} data-label-command="remove-all" onSelect={onRemoveAll}><span className="label-command-action-icon" aria-hidden="true"><X size={16}/></span><span className="label-command-name">{t('Remove all labels')}</span></Command.Item>}
      {model.selected.map(row)}
    </Command.Group>}
    {sections.map(section => <Fragment key={section.id}><Command.Group className="label-command-group" heading={t(section.heading)}>{section.entries.map(row)}</Command.Group></Fragment>)}
    {paths.length > 0 && <Command.Group>{paths.map(option => labelRow(option, true))}</Command.Group>}
  </>
}
