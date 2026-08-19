import { useEffect, useState, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Bell, Building2, Check, ChevronDown, ChevronRight, Copy, Download, Ellipsis, Layers3, LockKeyhole, Pencil, Trash2, UserRound } from 'lucide-react'
import { TeamIcon } from '@/components/issue/issue-icons'
import { DEFAULT_VIEW_COLOR, DEFAULT_VIEW_ICON, ViewIconPicker, type ViewVisual } from '@/components/views/view-icon-picker'
import type { SavedView, SavedViewMutationInput, Team, User } from '@/types/flow'
import styles from './saved-view-editor.module.css'

export type SavedViewTarget = { scope: SavedView['scope']; label: string; teamId?: string }

export function SavedViewEditor({ actions, ariaLabel = 'New view', initialName = '', namePlaceholder = 'All issues', initialDescription = '', initialIcon = DEFAULT_VIEW_ICON, initialColor = DEFAULT_VIEW_COLOR, initialTarget, saveTargets = [], saving = false, onCancel, onSave }: { actions?: ReactNode; ariaLabel?: string; initialName?: string; namePlaceholder?: string; initialDescription?: string; initialIcon?: string; initialColor?: string; initialTarget?: SavedViewTarget; saveTargets?: SavedViewTarget[]; saving?: boolean; onCancel: () => void; onSave: (name: string, description: string, target: SavedViewTarget | undefined, visual: ViewVisual) => void }) {
  const [name, setName] = useState(initialName), [description, setDescription] = useState(initialDescription)
  const [visual, setVisual] = useState<ViewVisual>({ icon: initialIcon, color: initialColor })
  const [target, setTarget] = useState(initialTarget ?? saveTargets[0])
  useEffect(() => { setName(initialName); setDescription(initialDescription); setVisual({ icon: initialIcon, color: initialColor }) }, [initialColor, initialDescription, initialIcon, initialName])
  const resolvedName = name.trim() || namePlaceholder
  const save = () => onSave(resolvedName, description.trim(), target, visual)
  return <section className={`${styles.editor} ${actions ? styles.withActions : ''}`} aria-label={ariaLabel}>
    <ViewIconPicker color={visual.color} icon={visual.icon} onChange={setVisual} triggerClassName={styles.icon}/>
    <input className={styles.name} aria-label="View name" autoFocus placeholder={namePlaceholder} value={name} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') save(); if (event.key === 'Escape') onCancel() }}/>
    {target && saveTargets.length > 0 && <div className={styles.saveTo}><span>Save to</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button" aria-label={`Save to ${target.label}`}>{target.scope === 'personal' ? <LockKeyhole size={12}/> : target.scope === 'team' ? <TeamIcon size={12}/> : <Building2 size={12}/>}<span data-i18n-ignore>{target.label}</span><ChevronDown size={11}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className={styles.targetMenu} align="end" collisionPadding={8} sideOffset={4}>{saveTargets.map((option, index) => <div key={`${option.scope}:${option.teamId ?? ''}`}>{option.scope === 'team' && saveTargets[index - 1]?.scope !== 'team' && <DropdownMenu.Separator className={styles.separator}/>}<DropdownMenu.CheckboxItem checked={sameTarget(option, target)} className={styles.targetItem} onCheckedChange={() => setTarget(option)}><span className={styles.targetCheck}><DropdownMenu.ItemIndicator><Check size={12}/></DropdownMenu.ItemIndicator></span>{option.scope === 'personal' ? <LockKeyhole size={13}/> : option.scope === 'team' ? <TeamIcon size={13}/> : <Building2 size={13}/>}<span data-i18n-ignore={option.scope === 'team' || undefined}>{option.label}</span></DropdownMenu.CheckboxItem></div>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>}
    <button className={styles.cancel} type="button" onClick={onCancel}>Cancel</button>
    <button className={styles.save} type="button" disabled={saving} onClick={save}>{saving ? 'Saving...' : 'Save'}</button>
    <input className={styles.description} aria-label="Description (optional)" placeholder="Description (optional)" value={description} onChange={event => setDescription(event.target.value)}/>
    {actions && <div className={styles.actions}>{actions}</div>}
  </section>
}

function sameTarget(left: SavedViewTarget, right: SavedViewTarget) { return left.scope === right.scope && left.teamId === right.teamId }

export function SavedViewMenu({ view, users = [], teams = [], subscriptionEvents = [], onEdit, onDuplicate, onUpdate, onSetSubscriptionEvents, onCopy, onExport, onDelete }: { view: SavedView; users?: User[]; teams?: Team[]; subscriptionEvents?: string[]; onEdit: () => void; onDuplicate?: () => void; onUpdate?: (input: SavedViewMutationInput) => void; onSetSubscriptionEvents?: (events: string[]) => void; onCopy?: () => void; onExport?: () => void; onDelete: () => void }) {
  const owner = users.find(user => user.id === view.ownerId) ?? users[0]
  const events = new Set(subscriptionEvents)
  const entity = view.resource === 'projects' ? 'A project' : 'An issue'
  const toggleEvent = (value: string) => {
    const next = new Set(events)
    if (next.has(value)) next.delete(value); else next.add(value)
    onSetSubscriptionEvents?.([...next])
  }
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className={styles.menuTrigger} type="button" aria-label={view.resource === 'projects' ? 'Project view options' : 'Issue view options'}><Ellipsis size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className={styles.menu} align="end" collisionPadding={8} sideOffset={4}>
    <DropdownMenu.Item className={styles.menuItem} onSelect={onEdit}><Pencil/>Edit…</DropdownMenu.Item>
    {onDuplicate && <DropdownMenu.Item className={styles.menuItem} onSelect={onDuplicate}><Copy/>Duplicate…</DropdownMenu.Item>}
    {onUpdate && <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}><UserRound/><span>Owner</span><ChevronRight className={styles.trailing}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className={`${styles.menu} ${styles.ownerMenu}`} sideOffset={-4}>{users.map(user => <DropdownMenu.Item className={styles.menuItem} key={user.id} onSelect={() => onUpdate({ ownerId: user.id })}><span className={styles.avatar}>{initials(user.displayName)}</span><span data-i18n-ignore>{user.displayName}</span>{owner?.id === user.id && <Check className={styles.trailing}/>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    {onUpdate && <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}><Layers3/><span>Move to</span><ChevronRight className={styles.trailing}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className={styles.menu} sideOffset={-4}><DropdownMenu.Item className={styles.menuItem} onSelect={() => onUpdate({ scope: 'personal', teamId: '' })}><LockKeyhole/>Personal</DropdownMenu.Item><DropdownMenu.Item className={styles.menuItem} onSelect={() => onUpdate({ scope: 'workspace', teamId: '' })}><Building2/>Workspace</DropdownMenu.Item>{teams.length > 0 && <DropdownMenu.Separator className={styles.separator}/>} {teams.map(team => <DropdownMenu.Item className={styles.menuItem} key={team.id} onSelect={() => onUpdate({ scope: 'team', teamId: team.id })}><TeamIcon/><span data-i18n-ignore>{team.name}</span></DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    <DropdownMenu.Separator className={styles.separator}/>
    {onSetSubscriptionEvents && <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}><Bell/><span>Subscribe</span><ChevronRight className={styles.trailing}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className={`${styles.menu} ${styles.subscriptionMenu}`} sideOffset={-4}><SubscriptionItem checked={events.has('issue-added')} label={`${entity} is added to the view`} onSelect={() => toggleEvent('issue-added')}/><SubscriptionItem checked={events.has('issue-completed')} label={`${entity} is marked completed or canceled`} onSelect={() => toggleEvent('issue-completed')}/></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    {(onCopy || onExport) && <DropdownMenu.Separator className={styles.separator}/>}
    {onCopy && <DropdownMenu.Item className={styles.menuItem} onSelect={onCopy}><Copy/>Copy link</DropdownMenu.Item>}
    {onExport && <DropdownMenu.Item className={styles.menuItem} onSelect={onExport}><Download/>Export {view.resource === 'projects' ? 'projects' : 'issues'} as CSV…</DropdownMenu.Item>}
    <DropdownMenu.Separator className={styles.separator}/>
    <DropdownMenu.Item className={styles.menuItem} data-danger onSelect={onDelete}><Trash2/>Delete</DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function SubscriptionItem({ checked, label, onSelect }: { checked: boolean; label: string; onSelect: () => void }) { return <DropdownMenu.CheckboxItem checked={checked} className={styles.menuItem} onSelect={event => { event.preventDefault(); onSelect() }}><span className={styles.checkbox}>{checked && <Check/>}</span><span>{label}</span></DropdownMenu.CheckboxItem> }
function initials(value: string) { return value.split(/\s+/).filter(Boolean).slice(0, 2).map(part => part[0]).join('').toUpperCase() }
