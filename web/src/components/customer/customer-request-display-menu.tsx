import * as Popover from '@radix-ui/react-popover'
import { ArrowDownUp } from 'lucide-react'
import { useState } from 'react'
import styles from '@/components/my-issues/my-issues-display-menu.module.css'
import { SelectControl } from '@/components/ui/select-control'
import { Toggle } from '@/components/ui/toggle'
import { FlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { DisplayOptionsIcon } from './customer-request-glyphs'
import { CUSTOMER_REQUEST_ORDERINGS, defaultOrderingDirection, type CustomerRequestViewPreferences } from './customer-request-model'

/**
 * Display options of the customers section (Linear's `uu`): Ordering — Created, Customer name or
 * Customer revenue, with its direction — and "Show important first".
 */
export function CustomerRequestDisplayMenu({ view, onChange, className }: { view: CustomerRequestViewPreferences; onChange: (view: CustomerRequestViewPreferences) => void; className?: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <FlowTooltip label={t('Display options')} disabled={open}>
      <Popover.Trigger asChild>
        <button type="button" className={className} aria-label={t('Display options')}><DisplayOptionsIcon size={14}/></button>
      </Popover.Trigger>
    </FlowTooltip>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" className={styles.popover} side="bottom" align="end" sideOffset={4} collisionPadding={11} aria-label={t('Display options')}>
        <section className={styles.section}>
          <div className={styles.groupingControl}>
            <span className={styles.rowLabel}>{t('Ordering')}</span>
            <div className={styles.groupingActions}>
              <button
                type="button"
                className={styles.orderButton}
                aria-label={`${t('Ordering direction')}: ${t(view.direction === 'asc' ? 'ascending' : 'descending')}`}
                title={t('Direction')}
                data-order={view.direction}
                onClick={() => onChange({ ...view, direction: view.direction === 'asc' ? 'desc' : 'asc' })}
              ><ArrowDownUp size={14}/></button>
              <SelectControl label={t('Ordering')} align="end" value={view.ordering} options={CUSTOMER_REQUEST_ORDERINGS.map(option => ({ value: option.value, label: t(option.label) }))} onChange={ordering => {
                const next = ordering as CustomerRequestViewPreferences['ordering']
                onChange({ ...view, ordering: next, direction: defaultOrderingDirection(next) })
              }}/>
            </div>
          </div>
          <div className={styles.switchRow}>
            <span>{t('Show important first')}</span>
            <Toggle checked={view.importantFirst} label={t('Show important first')} onChange={importantFirst => onChange({ ...view, importantFirst })}/>
          </div>
        </section>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}
