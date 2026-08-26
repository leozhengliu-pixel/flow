import { Check, ChevronDown } from 'lucide-react'
import type { ReactNode } from 'react'

import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Toggle } from '@/components/ui/toggle'

export function SettingsPageTitle({ action, children, className = '', description }: { action?: ReactNode; children: ReactNode; className?: string; description?: ReactNode }) {
  return <header className={`settings-page-header${className ? ` ${className}` : ''}`}><div><h1>{children}</h1>{description && <p>{description}</p>}</div>{action}</header>
}

export function SettingsSection({ action, children, className = '', description, headerClassName = '', title }: { action?: ReactNode; children: ReactNode; className?: string; description?: ReactNode; headerClassName?: string; title?: ReactNode }) {
  return <section className={`settings-section${className ? ` ${className}` : ''}`}>{action ? <header className={headerClassName || 'settings-section-title'}>{title && <h3>{title}</h3>}{action}</header> : title && <h3>{title}</h3>}{description && <p className="settings-section-description">{description}</p>}<div className="settings-card">{children}</div></section>
}

export function SettingsRow({ children, className = '', control = true, danger = false, description, icon, title }: { children?: ReactNode; className?: string; control?: boolean; danger?: boolean; description?: ReactNode; icon?: ReactNode; title: ReactNode }) {
  return <div className={`settings-row${danger ? ' danger' : ''}${className ? ` ${className}` : ''}`}>{icon && <span className="settings-row-icon">{icon}</span>}<div><strong>{title}</strong>{description && <span>{description}</span>}</div>{children && (control ? <div className="settings-control">{children}</div> : children)}</div>
}

export function SettingsToggle({ checked, disabled, label, onChange }: { checked: boolean; disabled?: boolean; label: string; onChange: (value: boolean) => void | Promise<void> }) {
  return <Toggle checked={checked} disabled={disabled} label={label} onChange={onChange} size="regular"/>
}

export function SettingsSelect({ className = '', entityName, label, menuClassName = '', onChange, options, value }: { className?: string; entityName?: (value: string) => boolean; label: string; menuClassName?: string; onChange: (value: string) => void; options: string[]; value: string }) {
  const text = (option: string) => <span data-i18n-ignore={entityName?.(option) || undefined}>{option}</span>
  return <DropdownMenu><DropdownMenuTrigger asChild><button className={className || 'settings-select'} aria-label={label}>{text(value)}<ChevronDown size={13}/></button></DropdownMenuTrigger><DropdownMenuContent align="end" className={menuClassName || 'settings-select-menu'}>{options.map(option => <DropdownMenuItem key={option} onSelect={() => onChange(option)}>{text(option)}<Check size={13} className={option === value ? 'selected-check' : 'hidden-check'}/></DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
}
