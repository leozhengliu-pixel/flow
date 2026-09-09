import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { useRef, useState, type ReactNode } from 'react'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import './project-action-menu.css'

export function ProjectMenuItem({ label, icon, shortcut, onSelect }: { label: string; icon: ReactNode; shortcut?: string; onSelect: () => void }) {
  const { t } = useI18n()
  return <DropdownMenu.Item onSelect={onSelect}><span className="project-menu-icon"><IssueActionGlyph label={label === 'Copy overview as Markdown' ? 'Copy content as Markdown' : label} fallback={icon}/></span><span className="project-menu-label">{t(label)}</span>{shortcut && <ProjectMenuShortcut value={shortcut}/>}</DropdownMenu.Item>
}

export function ProjectSubmenu({ label, icon, shortcut, children, searchable = false, alignOffset, className = '' }: { label: string; icon: ReactNode; shortcut?: string; children: ReactNode | ((close: () => void) => ReactNode); searchable?: boolean; alignOffset?: number; className?: string }) {
  const [open, setOpen] = useState(false)
  const trigger = useRef<HTMLDivElement>(null)
  const { t } = useI18n()
  return <DropdownMenu.Sub open={open} onOpenChange={setOpen}>
    <DropdownMenu.SubTrigger ref={trigger}><span className="project-menu-icon">{label === 'Dependencies' ? <ProjectDependencyIcon/> : label === 'Initiatives' ? <ViewGlyph icon="Initiative" color="currentColor"/> : <IssueActionGlyph label={label === 'Blocked by' || label === 'Blocking' ? `${label}…` : label} fallback={icon}/>}</span><span className="project-menu-label">{t(label)}</span>{shortcut && <ProjectMenuShortcut value={shortcut}/>}<span className="project-menu-chevron" aria-hidden="true">▶</span></DropdownMenu.SubTrigger>
    <DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className={`project-action-menu project-action-submenu ${className}`} data-menu={label} aria-label={t(label)} sideOffset={-2} alignOffset={alignOffset ?? (searchable ? -43 : -6.5)} collisionPadding={16} onFocusOutside={event => {
      // Radix briefly focuses the parent menu when the pointer crosses the gap.
      const target = event.target
      if (target instanceof HTMLElement && target.matches('[role=menu]') && target.contains(trigger.current)) event.preventDefault()
    }} onKeyDown={event => { if ((event.target as HTMLElement).closest('input,[cmdk-root]') && !['ArrowLeft','Escape'].includes(event.key)) event.stopPropagation() }}>
      {typeof children === 'function' ? children(() => { setOpen(false); trigger.current?.focus() }) : children}
    </DropdownMenu.SubContent></DropdownMenu.Portal>
  </DropdownMenu.Sub>
}

export function ProjectMenuShortcut({ value }: { value: string }) {
  const { t } = useI18n()
  return <span className="project-menu-shortcut" aria-hidden="true">{value.includes('then') ? value.replace('then', t('then')) : value.split(' ').map((key,index) => <kbd key={index}>{key}</kbd>)}</span>
}

export function ProjectMenuSearch({ query, onChange, label }: { query:string; onChange:(value:string)=>void; label:string }) {
  const {t} = useI18n()
  return <div className={`project-action-menu__search${query ? '' : ' is-hidden'}`}><input autoFocus value={query} aria-label={t(label)} placeholder={t('Filter…')} onChange={event => onChange(event.target.value)} onKeyDown={event => {
    if (event.key === 'Escape') return
    event.stopPropagation()
    if (event.key !== 'ArrowDown' && event.key !== 'Enter') return
    const first = event.currentTarget.closest('[role=menu]')?.querySelector<HTMLElement>('[role=menuitem]:not([data-disabled])')
    if (first) { event.preventDefault(); if (event.key === 'Enter') first.click(); else first.focus() }
  }}/></div>
}

function ProjectDependencyIcon() {
  return <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M7.5 2.857a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v1a1 1 0 0 1-1 1h-6a1 1 0 0 1-1-1v-1ZM10.75 5.857h1.5v1.5A2.25 2.25 0 0 1 10 9.607H7a.75.75 0 0 0-.75.75v1.5h-1.5v-1.5A2.25 2.25 0 0 1 7 8.107h3a.75.75 0 0 0 .75-.75v-1.5ZM2.5 12.857a1 1 0 0 0-1 1v1a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1v-1a1 1 0 0 0-1-1h-6Z"/></svg>
}
