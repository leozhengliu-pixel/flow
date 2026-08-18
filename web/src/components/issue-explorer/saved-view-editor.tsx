import { useEffect, useState, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Building2, Check, ChevronDown, Ellipsis, LockKeyhole, Pencil, Trash2 } from 'lucide-react'
import { TeamIcon } from '@/components/issue/issue-icons'
import { DEFAULT_VIEW_COLOR, DEFAULT_VIEW_ICON, ViewIconPicker, type ViewVisual } from '@/components/views/view-icon-picker'
import type { SavedView } from '@/types/flow'
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
    {target && saveTargets.length > 0 && <div className={styles.saveTo}><span>Save to</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button" aria-label={`Save to ${target.label}`}>{target.scope === 'personal' ? <LockKeyhole size={12}/> : target.scope === 'team' ? <TeamIcon size={12}/> : <Building2 size={12}/>}<span>{target.label}</span><ChevronDown size={11}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className={styles.targetMenu} align="end" sideOffset={4}>{saveTargets.map(option => <DropdownMenu.CheckboxItem checked={sameTarget(option, target)} className={styles.targetItem} key={`${option.scope}:${option.teamId ?? ''}`} onCheckedChange={() => setTarget(option)}><span className={styles.targetCheck}><DropdownMenu.ItemIndicator><Check size={12}/></DropdownMenu.ItemIndicator></span>{option.scope === 'personal' ? <LockKeyhole size={13}/> : option.scope === 'team' ? <TeamIcon size={13}/> : <Building2 size={13}/>}<span>{option.label}</span></DropdownMenu.CheckboxItem>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>}
    <button className={styles.cancel} type="button" onClick={onCancel}>Cancel</button>
    <button className={styles.save} type="button" disabled={saving} onClick={save}>{saving ? 'Saving...' : 'Save'}</button>
    <input className={styles.description} aria-label="Description (optional)" placeholder="Description (optional)" value={description} onChange={event => setDescription(event.target.value)}/>
    {actions && <div className={styles.actions}>{actions}</div>}
  </section>
}

function sameTarget(left: SavedViewTarget, right: SavedViewTarget) { return left.scope === right.scope && left.teamId === right.teamId }

export function SavedViewMenu({ view: _view, onEdit, onUpdate, onDelete }: { view: SavedView; onEdit: () => void; onUpdate: () => void; onDelete: () => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className={styles.menuTrigger} type="button" aria-label="Saved view menu"><Ellipsis size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className={styles.menu} align="end" sideOffset={4}>
    <DropdownMenu.Item className={styles.menuItem} onSelect={onUpdate}><Check size={14}/>Save current view</DropdownMenu.Item>
    <DropdownMenu.Item className={styles.menuItem} onSelect={onEdit}><Pencil size={14}/>Edit name and description</DropdownMenu.Item>
    <DropdownMenu.Separator className={styles.separator}/>
    <DropdownMenu.Item className={styles.menuItem} data-danger onSelect={onDelete}><Trash2 size={14}/>Delete view</DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}
