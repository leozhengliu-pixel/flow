import * as ContextMenu from '@radix-ui/react-context-menu'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'

import { PulseIcon } from '@/components/pulse/pulse-icon'
import { useI18n } from '@/i18n/i18n'

import { PULSE_FREQUENCIES, type PulseFrequency } from './pulse-summary-model'
import './pulse-summary-view.css'

type Props = {
  value: PulseFrequency
  disabled?: boolean
  onChange: (value: PulseFrequency) => void
}

/** "Pulse frequency" submenu (Daily / Weekly / Never) in the inbox row context menu. */
export function PulseFrequencyContextSubmenu({ value, disabled, onChange }: Props) {
  const { t } = useI18n()
  return (
    <ContextMenu.Sub>
      <ContextMenu.SubTrigger className="flow-inbox-menu__item" disabled={disabled}>
        <span className="flow-inbox-menu__item-icon"><PulseIcon /></span>
        <span className="flow-inbox-menu__item-label">{t('Pulse frequency')}</span>
        <span className="flow-inbox-menu__trailing">▶</span>
      </ContextMenu.SubTrigger>
      <ContextMenu.Portal>
        <ContextMenu.SubContent data-flow-motion="floating" className="flow-inbox-menu flow-inbox-pulse-frequency-menu" sideOffset={4} aria-label={t('Pulse frequency')}>
          <ContextMenu.RadioGroup value={value} onValueChange={next => onChange(next as PulseFrequency)}>
            {PULSE_FREQUENCIES.map(option => (
              <ContextMenu.RadioItem key={option.value} value={option.value} className="flow-inbox-menu__item" disabled={disabled}>
                <span className="flow-inbox-menu__item-label">{t(option.label)}</span>
                <ContextMenu.ItemIndicator className="flow-inbox-menu__trailing"><CheckIcon /></ContextMenu.ItemIndicator>
              </ContextMenu.RadioItem>
            ))}
          </ContextMenu.RadioGroup>
        </ContextMenu.SubContent>
      </ContextMenu.Portal>
    </ContextMenu.Sub>
  )
}

/** The same submenu inside the detail pane's "…" dropdown. */
export function PulseFrequencyDropdownSubmenu({ value, disabled, onChange }: Props) {
  const { t } = useI18n()
  return (
    <DropdownMenu.Sub>
      <DropdownMenu.SubTrigger className="flow-inbox-menu__item" disabled={disabled}>
        <span className="flow-inbox-menu__item-icon"><PulseIcon /></span>
        <span className="flow-inbox-menu__item-label">{t('Pulse frequency')}</span>
        <span className="flow-inbox-menu__trailing">▶</span>
      </DropdownMenu.SubTrigger>
      <DropdownMenu.Portal>
        <DropdownMenu.SubContent data-flow-motion="floating" className="flow-inbox-menu flow-inbox-pulse-frequency-menu" sideOffset={4} aria-label={t('Pulse frequency')}>
          <DropdownMenu.RadioGroup value={value} onValueChange={next => onChange(next as PulseFrequency)}>
            {PULSE_FREQUENCIES.map(option => (
              <DropdownMenu.RadioItem key={option.value} value={option.value} className="flow-inbox-menu__item" disabled={disabled}>
                <span className="flow-inbox-menu__item-label">{t(option.label)}</span>
                <DropdownMenu.ItemIndicator className="flow-inbox-menu__trailing"><CheckIcon /></DropdownMenu.ItemIndicator>
              </DropdownMenu.RadioItem>
            ))}
          </DropdownMenu.RadioGroup>
        </DropdownMenu.SubContent>
      </DropdownMenu.Portal>
    </DropdownMenu.Sub>
  )
}

function CheckIcon() {
  return <svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m3.5 8.5 3 3 6-7" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
}
