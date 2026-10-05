import * as Popover from '@radix-ui/react-popover'
import { Check } from 'lucide-react'
import { useState } from 'react'
import { ViewIconPicker, type ViewVisual } from '@/components/views/view-icon-picker'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { SubscriptionIcon } from '@/components/ui/view-action-icons'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { PulseFilterChips, PulseFilterMenu, PulseMatchSummary } from './pulse-filters'
import { PULSE_SCHEDULES, pulseScheduleLabels, type PulseSchedule, type PulseViewDraft } from './pulse-schedule'

/** Linear's bell: "Inbox notifications for Pulse summaries" Daily / Weekly / Never (330px). */
export function PulseSubscriptionMenu({ cadence, onChange }: { cadence: PulseSchedule; onChange: (cadence: PulseSchedule) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const options = PULSE_SCHEDULES.map(value => ({ id: value, label: t(pulseScheduleLabels[value]) }))
  const command = usePropertyCommand({ open, options, selectedIds: [cadence], onOpenChange: setOpen, onSelect: option => { onChange(option.id as PulseSchedule); setOpen(false) } })
  return <Popover.Root open={open} onOpenChange={next => { setOpen(next); if (!next) command.onQueryChange('') }}><Popover.Trigger asChild><button aria-label={t('Subscription')} className="pulse-icon-button" data-subscribed={cadence !== 'never' || undefined} type="button"><SubscriptionIcon/></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align="end" className="pulse-subscription-menu" collisionPadding={8} data-has-query={Boolean(command.query) || undefined} onKeyDown={command.onKeyDown} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => command.inputRef.current?.focus()) }} sideOffset={3.5}>
    <span aria-live="polite" className="sr-only" role="status">{command.filteredOptions.length === options.length ? t('Showing all items') : `Showing ${command.filteredOptions.length} ${command.filteredOptions.length === 1 ? 'item' : 'items'}`}</span>
    <input aria-activedescendant={command.activeId ? `pulse-subscription-${command.activeId}` : undefined} aria-controls="pulse-subscription-options" aria-label={t('Filter…')} className="pulse-subscription-search" placeholder={t('Filter…')} ref={command.inputRef} role="searchbox" value={command.query} onChange={event => command.onQueryChange(event.target.value)}/>
    <div aria-multiselectable="false" id="pulse-subscription-options" role="listbox">
      {!command.query && <div className="pulse-subscription-label">{t('Inbox notifications for Pulse summaries')}</div>}
      {command.filteredOptions.map(option => <button aria-checked={cadence === option.id} aria-selected={command.activeId === option.id} id={`pulse-subscription-${option.id}`} key={option.id} onClick={() => command.choose(option)} onFocus={() => command.setActiveId(option.id)} onPointerMove={() => command.setActiveId(option.id)} role="option" type="button"><span>{option.label}</span>{cadence === option.id && <Check size={13}/>}</button>)}
    </div>
  </Popover.Content></Popover.Portal></Popover.Root>
}

export function PulseNewViewEditor({ data,draft,onCancel,onChange,onSave,saving=false }: { data:BootstrapData;draft:PulseViewDraft;onCancel:()=>void;onChange:(draft:PulseViewDraft)=>void;onSave:()=>void;saving?:boolean }) {
  const { t } = useI18n()
  return <form className="pulse-new-view" onSubmit={event => { event.preventDefault(); onSave() }}>
    <div className="pulse-new-view-top"><div className="pulse-new-view-identity"><ViewIconPicker color={draft.color} icon={draft.icon} onChange={(visual: ViewVisual) => onChange({ ...draft, ...visual })}/><input aria-label={t('View name')} autoFocus placeholder={t('All updates')} value={draft.name} onChange={event => onChange({ ...draft, name: event.target.value })} onKeyDown={event => { if (event.key === 'Escape') onCancel() }}/></div><div className="pulse-new-view-actions"><button className="pulse-new-view-cancel" onClick={onCancel} type="button">{t('Cancel')}</button><button className="pulse-new-view-save" disabled={saving} type="submit">{t('Save')}</button></div></div>
    <div className="pulse-new-view-filters"><PulseMatchSummary count={draft.filters.length} match={draft.match}/><PulseFilterChips data={data} filters={draft.filters} onChange={filters=>onChange({...draft,filters})}/><span/><PulseFilterMenu align="end" compact data={data} filters={draft.filters} match={draft.match} onChange={filters=>onChange({...draft,filters})} onMatchChange={match=>onChange({...draft,match})}/></div>
  </form>
}
