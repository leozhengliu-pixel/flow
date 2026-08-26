import type { ChangeEvent } from 'react'

import styles from './toggle.module.css'

export function Toggle({ checked, className = '', disabled = false, label, onChange, size = 'compact' }: {
  checked: boolean
  className?: string
  disabled?: boolean
  label: string
  onChange: (checked: boolean) => void | Promise<void>
  size?: 'compact' | 'regular'
}) {
  const change = (event: ChangeEvent<HTMLInputElement>) => void onChange(event.target.checked)
  return <input aria-label={label} checked={checked} className={`${styles.toggle} ${styles[size]}${className ? ` ${className}` : ''}`} data-control="toggle" disabled={disabled} onChange={change} type="checkbox"/>
}
