import * as Select from '@radix-ui/react-select'
import type { ReactNode } from 'react'

import './select-control.css'

export type SelectControlOption = {
  value: string
  label: string
  disabled?: boolean
  entityName?: boolean
  groupLabel?: string
  icon?: ReactNode
}

const EMPTY_VALUE = '__flow_select_empty__'

export function SelectControl({
  align = 'start',
  className = '',
  disabled = false,
  label,
  onChange,
  options,
  value,
}: {
  align?: 'start' | 'center' | 'end'
  className?: string
  disabled?: boolean
  label: string
  onChange: (value: string) => void
  options: SelectControlOption[]
  value: string
}) {
  const selected = options.find(option => option.value === value)
  const groups = [...new Set(options.map(option => option.groupLabel).filter(Boolean))] as string[]
  const renderOption = (option: SelectControlOption) => <Select.Item className="select-control-option" disabled={option.disabled} key={option.value || EMPTY_VALUE} value={option.value || EMPTY_VALUE}>
    <Select.ItemText><span data-i18n-ignore={option.entityName || undefined}>{option.icon}{option.label}</span></Select.ItemText>
    <Select.ItemIndicator><CheckIcon/></Select.ItemIndicator>
  </Select.Item>
  return <Select.Root disabled={disabled} onValueChange={next => onChange(next === EMPTY_VALUE ? '' : next)} value={value || EMPTY_VALUE}>
    <Select.Trigger aria-label={label} className={`select-control ${className}`.trim()}>
      <Select.Value>
        <span data-i18n-ignore={selected?.entityName || undefined}>{selected?.icon}{selected?.label ?? value}</span>
      </Select.Value>
      <Select.Icon><ChevronIcon/></Select.Icon>
    </Select.Trigger>
    <Select.Portal>
      <Select.Content align={align} className="select-control-menu" collisionPadding={8} position="popper" sideOffset={4}>
        <Select.Viewport>
          {options.filter(option => !option.groupLabel).map(renderOption)}
          {groups.map(group => <Select.Group key={group}>
            <Select.Label className="select-control-group-label">{group}</Select.Label>
            {options.filter(option => option.groupLabel === group).map(renderOption)}
          </Select.Group>)}
        </Select.Viewport>
      </Select.Content>
    </Select.Portal>
  </Select.Root>
}

function ChevronIcon() {
  return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M3.46967 5.46967C3.76256 5.17678 4.23744 5.17678 4.53033 5.46967L8 8.93934L11.4697 5.46967C11.7626 5.17678 12.2374 5.17678 12.5303 5.46967C12.8232 5.76256 12.8232 6.23744 12.5303 6.53033L8.53033 10.5303C8.23744 10.8232 7.76256 10.8232 7.46967 10.5303L3.46967 6.53033C3.17678 6.23744 3.17678 5.76256 3.46967 5.46967Z"/></svg>
}

function CheckIcon() {
  return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M13.5303 4.46967C13.8232 4.76256 13.8232 5.23744 13.5303 5.53033L7.03033 12.0303C6.73744 12.3232 6.26256 12.3232 5.96967 12.0303L2.46967 8.53033C2.17678 8.23744 2.17678 7.76256 2.46967 7.46967C2.76256 7.17678 3.23744 7.17678 3.53033 7.46967L6.5 10.4393L12.4697 4.46967C12.7626 4.17678 13.2374 4.17678 13.5303 4.46967Z"/></svg>
}
