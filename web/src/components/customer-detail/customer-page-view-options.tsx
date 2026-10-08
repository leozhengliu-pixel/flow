import * as Popover from '@radix-ui/react-popover'
import * as Select from '@radix-ui/react-select'
import { Check, ChevronDown } from 'lucide-react'

import styles from '@/components/my-issues/my-issues-display-menu.module.css'
import { FlowTooltip } from '@/components/ui/tooltip'
import { Toggle } from '@/components/ui/toggle'
import { DisplayIcon } from '@/components/ui/view-action-icons'
import { useI18n } from '@/i18n/i18n'
import type { CustomerPageCompletedWindow, CustomerPageGrouping, CustomerPageOrdering, CustomerPageViewPreferences } from './customer-page-model'

const GROUPING_OPTIONS: Array<{ value: CustomerPageGrouping; label: string }> = [
  { value: 'statusType', label: 'Status type' },
  { value: 'none', label: 'None' },
]
const ORDERING_OPTIONS: Array<{ value: CustomerPageOrdering; label: string }> = [
  { value: 'createdAt', label: 'Created date' },
  { value: 'statusType', label: 'Status type' },
]
const COMPLETED_OPTIONS: Array<{ value: CustomerPageCompletedWindow; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'day', label: 'Past day' },
  { value: 'week', label: 'Past week' },
  { value: 'month', label: 'Past month' },
  { value: 'none', label: 'None' },
]
const PROPERTIES: Array<{ key: 'fieldIdentifier' | 'fieldPriority' | 'fieldStatus' | 'fieldTargetDueDate'; label: string }> = [
  { key: 'fieldIdentifier', label: 'ID' },
  { key: 'fieldPriority', label: 'Priority' },
  { key: 'fieldStatus', label: 'Status' },
  { key: 'fieldTargetDueDate', label: 'Target/Due date' },
]

/**
 * Linear's customer page view options (CustomerPage.Bn): Grouping (Status type / None), Ordering
 * (Created date / Status type), Completed, Show important first and the four display properties.
 */
export function CustomerPageViewOptions({ view, onChange, open, onOpenChange }: { view: CustomerPageViewPreferences; onChange: (view: CustomerPageViewPreferences) => void; open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const change = (patch: Partial<CustomerPageViewPreferences>) => onChange({ ...view, ...patch })
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <FlowTooltip label={t('Display options')} disabled={open}>
      <Popover.Trigger asChild>
        <button type="button" className="customer-needs__display" aria-label={t('Display options')} aria-pressed={open}><DisplayIcon/></button>
      </Popover.Trigger>
    </FlowTooltip>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" className={styles.popover} side="bottom" align="end" sideOffset={3} collisionPadding={11} aria-label={t('Display options')}>
        <section className={styles.section}>
          <SelectField label="Grouping" value={view.grouping} options={GROUPING_OPTIONS} onChange={grouping => change(grouping === 'statusType' && view.ordering === 'statusType' ? { grouping, ordering: 'createdAt' } : { grouping })}/>
          <SelectField label="Ordering" value={view.ordering} options={ORDERING_OPTIONS} onChange={ordering => change(ordering === 'statusType' ? { ordering, fieldStatus: true } : { ordering })}/>
        </section>
        <section className={styles.section}>
          <SelectField label="Completed" value={view.completed} options={COMPLETED_OPTIONS} onChange={completed => change({ completed })}/>
          <div className={styles.switchRow}>
            <span>{t('Show important first')}</span>
            <Toggle checked={view.importantFirst} label={t('Show important first')} onChange={importantFirst => change({ importantFirst })}/>
          </div>
        </section>
        <section className={styles.section}>
          <span className={styles.sectionLabel}>{t('Display properties')}</span>
          <div className={styles.propertyGrid}>
            {PROPERTIES.map(property => <button key={property.key} type="button" data-active={view[property.key]} aria-pressed={view[property.key]} onClick={() => change({ [property.key]: !view[property.key] })}><span>{t(property.label)}</span></button>)}
          </div>
        </section>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function SelectField<T extends string>({ label, onChange, options, value }: { label: string; onChange: (value: T) => void; options: Array<{ value: T; label: string }>; value: T }) {
  const { t } = useI18n()
  return <label className={styles.selectField}>
    <span>{t(label)}</span>
    <Select.Root value={value} onValueChange={next => onChange(next as T)}>
      <Select.Trigger className={styles.selectTrigger} aria-label={t(label)}>
        <Select.Value/><Select.Icon><ChevronDown size={12} aria-hidden="true"/></Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Content data-flow-motion="floating" className={styles.selectMenu} position="popper" side="bottom" align="end" sideOffset={5} collisionPadding={10}>
          <Select.Viewport className={styles.selectViewport}>
            {options.map(option => <Select.Item className={styles.selectItem} key={option.value} value={option.value}>
              <Select.ItemText>{t(option.label)}</Select.ItemText>
              <Select.ItemIndicator className={styles.selectIndicator}><Check size={13} aria-hidden="true"/></Select.ItemIndicator>
            </Select.Item>)}
          </Select.Viewport>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  </label>
}
