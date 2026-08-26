import type { ReactNode } from 'react'
import { CheckboxMark } from './checkbox-mark'

export function NotificationOptionSection({ children, className, title }: { children: ReactNode; className: string; title: ReactNode }) { return <section className={className}><h2>{title}</h2>{children}</section> }
export function NotificationCheckbox({ checked, label, onChange }: { checked: boolean; label: string; onChange: (checked: boolean) => void }) { return <label><span>{label}</span><button aria-checked={checked} aria-label={label} data-checked={checked} onClick={() => onChange(!checked)} role="checkbox" type="button">{checked && <CheckboxMark/>}</button></label> }
