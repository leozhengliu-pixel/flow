import { useEffect, useRef, useState, type ReactNode } from 'react'
import { PeopleMenuItems } from '@/components/property/people-menu-items'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Bell, Building2, ChevronRight, Copy, Download, Ellipsis, Layers3, LockKeyhole, Pencil, Trash2, UserRound } from 'lucide-react'
import { TeamIcon } from '@/components/issue/issue-icons'
import { DEFAULT_VIEW_COLOR, DEFAULT_VIEW_ICON, ViewIconPicker, type ViewVisual } from '@/components/views/view-icon-picker'
import type { SavedView, SavedViewMutationInput, Team, User } from '@/types/flow'
import styles from './saved-view-editor.module.css'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { DropdownMenuContent } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'

export type SavedViewTarget = { scope: SavedView['scope']; label: string; teamId?: string; team?: Pick<Team, 'icon' | 'color'>; labelIsEntityName?: boolean }
export type SavedViewDraft = { name: string; description: string; visual: ViewVisual }

/**
 * Linear's view card (new view and "Edit…"): icon, name, description, "Save to" scope, Cancel and
 * Save / Create view, and a bottom section (filter chips + filter / display buttons) passed as `actions`.
 * Suggestions from the filters fill the name placeholder, an empty description and a default icon.
 */
export function SavedViewEditor({ actions, ariaLabel = 'New view', mode = 'create', initialName = '', namePlaceholder = 'All issues', suggestedName, suggestedDescription, suggestedIcon, initialDescription = '', initialIcon = DEFAULT_VIEW_ICON, initialColor = DEFAULT_VIEW_COLOR, initialTarget, saveTargets = [], saving = false, onCancel, onDraftChange, onSave }: {
  actions?: ReactNode
  ariaLabel?: string
  mode?: 'create' | 'edit'
  initialName?: string
  namePlaceholder?: string
  /** Shown as the name placeholder only; used as the name when it is left empty. */
  suggestedName?: string
  /** Filled in as the real description while the description is empty. */
  suggestedDescription?: string
  /** Applied while the icon is still the default. */
  suggestedIcon?: string
  initialDescription?: string
  initialIcon?: string
  initialColor?: string
  initialTarget?: SavedViewTarget
  saveTargets?: SavedViewTarget[]
  saving?: boolean
  onCancel: () => void
  /** Live draft for the breadcrumb and details panel (Linear updates them as you type). */
  onDraftChange?: (draft: SavedViewDraft) => void
  onSave: (name: string, description: string, target: SavedViewTarget | undefined, visual: ViewVisual) => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState(initialName), [description, setDescription] = useState(initialDescription)
  const [descriptionTouched, setDescriptionTouched] = useState(Boolean(initialDescription))
  const [visual, setVisual] = useState<ViewVisual>({ icon: initialIcon, color: initialColor })
  const [iconTouched, setIconTouched] = useState(initialIcon !== DEFAULT_VIEW_ICON)
  const [target, setTarget] = useState(initialTarget ?? saveTargets[0])
  useEffect(() => { setName(initialName); setDescription(initialDescription); setDescriptionTouched(Boolean(initialDescription)); setVisual({ icon: initialIcon, color: initialColor }); setIconTouched(initialIcon !== DEFAULT_VIEW_ICON) }, [initialColor, initialDescription, initialIcon, initialName])
  useEffect(() => { if (initialTarget) setTarget(initialTarget) }, [initialTarget])
  const shownDescription = descriptionTouched ? description : (description || suggestedDescription || '')
  const shownIcon = iconTouched || !suggestedIcon ? visual.icon : suggestedIcon, shownColor = visual.color
  const shownVisual = { icon: shownIcon, color: shownColor }
  const draftRef = useRef(onDraftChange); draftRef.current = onDraftChange
  useEffect(() => { draftRef.current?.({ name, description: shownDescription, visual: { icon: shownIcon, color: shownColor } }) }, [name, shownDescription, shownIcon, shownColor])
  const placeholder = suggestedName || t(namePlaceholder)
  const resolvedName = name.trim() || placeholder
  const save = () => { if (!saving) onSave(resolvedName, shownDescription.trim(), target, shownVisual) }
  // Escape (with no popover open) leaves the editor and discards the draft.
  const cancelRef = useRef(onCancel); cancelRef.current = onCancel
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || event.isComposing) return
      if (document.querySelector('[data-radix-popper-content-wrapper], [role="dialog"], [role="alertdialog"]')) return
      event.preventDefault()
      cancelRef.current()
    }
    addEventListener('keydown', onKey, true)
    return () => removeEventListener('keydown', onKey, true)
  }, [])
  const targetLabel = (option: SavedViewTarget) => option.scope === 'team' || option.labelIsEntityName || !['Personal', 'Workspace'].includes(option.label) ? option.label : t(option.label)
  return <section className={styles.editor} data-mode={mode} aria-label={t(ariaLabel)}>
    <div className={styles.header}>
      <ViewIconPicker ariaLabel={t('Choose icon')} color={shownVisual.color} icon={shownVisual.icon} onChange={next => { setIconTouched(true); setVisual(next) }} triggerClassName={styles.icon}/>
      <div className={styles.fields}>
        <input className={styles.name} aria-label={t('View name')} autoFocus={mode === 'create'} placeholder={placeholder} value={name} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && !event.nativeEvent.isComposing) save() }}/>
        <input className={styles.description} aria-label={t('Description (optional)')} placeholder={t('Description (optional)')} value={shownDescription} onChange={event => { setDescriptionTouched(true); setDescription(event.target.value) }}/>
      </div>
      <div className={styles.commands}>
        {target && saveTargets.length > 0 && <div data-i18n-ignore className={styles.saveTo}>
          <span>{t('Save to')}</span>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button type="button" disabled={saving} aria-label={`${t('Save to')} ${targetLabel(target)}`}>
                {target.scope === 'personal' ? <LockKeyhole size={12}/> : target.scope === 'team' ? <TeamIcon team={target.team} size={12}/> : <Building2 size={12}/>}
                <span>{targetLabel(target)}</span>
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenuContent data-i18n-ignore className={styles.targetMenu} align="end" collisionPadding={8} sideOffset={4}>
              <DropdownMenu.RadioGroup value={targetKey(target)} onValueChange={value => { const option = saveTargets.find(item => targetKey(item) === value); if (option) setTarget(option) }}>
                {saveTargets.map((option, index) => <div key={targetKey(option)}>
                  {option.scope === 'team' && saveTargets[index - 1]?.scope !== 'team' && <DropdownMenu.Separator className={styles.separator}/>}
                  <DropdownMenu.RadioItem value={targetKey(option)} textValue={targetLabel(option)} className={styles.targetItem}>
                    {option.scope === 'personal' ? <LockKeyhole size={13}/> : option.scope === 'team' ? <TeamIcon team={option.team} size={13}/> : <Building2 size={13}/>}
                    <span className={styles.targetName}>{targetLabel(option)}</span>
                    <span className={styles.targetCheck}><DropdownMenu.ItemIndicator><CheckboxMark/></DropdownMenu.ItemIndicator></span>
                  </DropdownMenu.RadioItem>
                </div>)}
              </DropdownMenu.RadioGroup>
            </DropdownMenuContent>
          </DropdownMenu.Root>
        </div>}
        <button className={styles.cancel} type="button" onClick={onCancel}>{t('Cancel')}</button>
        <button className={styles.save} type="button" disabled={saving} onClick={save}>{t(saving ? 'Saving...' : mode === 'edit' ? 'Save' : 'Create view')}</button>
      </div>
    </div>
    {actions && <div className={styles.actions}>{actions}</div>}
  </section>
}

function targetKey(target: SavedViewTarget) { return `${target.scope}:${target.teamId ?? ''}` }

export function SavedViewMenu({ view, users = [], teams = [], subscriptionEvents = [], onEdit, onDuplicate, onUpdate, onSetSubscriptionEvents, onShare, onCopy, onExport, onDelete }: { view: SavedView; users?: User[]; teams?: Team[]; subscriptionEvents?: string[]; onEdit: () => void; onDuplicate?: () => void; onUpdate?: (input: SavedViewMutationInput) => void; onSetSubscriptionEvents?: (events: string[]) => void; onShare?: () => void; onCopy?: () => void; onExport?: () => void; onDelete: () => void }) {
  const owner = users.find(user => user.id === view.ownerId) ?? users[0]
  const events = new Set(subscriptionEvents)
  const entity = view.resource === 'projects' ? 'A project' : 'An issue'
  const toggleEvent = (value: string) => {
    const next = new Set(events)
    if (next.has(value)) next.delete(value); else next.add(value)
    onSetSubscriptionEvents?.([...next])
  }
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className={styles.menuTrigger} type="button" aria-label={view.resource === 'projects' ? 'Project view options' : 'Issue view options'}><Ellipsis size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" className={styles.menu} align="end" collisionPadding={8} sideOffset={4}>
    <DropdownMenu.Item className={styles.menuItem} onSelect={onEdit}><Pencil/>Edit…</DropdownMenu.Item>
    {onDuplicate && <DropdownMenu.Item className={styles.menuItem} onSelect={onDuplicate}><Copy/>Duplicate…</DropdownMenu.Item>}
    {onUpdate && <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}><UserRound/><span>Owner</span><ChevronRight className={styles.trailing}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className={`${styles.menu} ${styles.ownerMenu}`} sideOffset={-4}><PeopleMenuItems users={users} selectedId={owner?.id} itemClassName={styles.menuItem} onSelect={id => onUpdate({ ownerId: id })}/></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    {onUpdate && <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}><Layers3/><span>Move to</span><ChevronRight className={styles.trailing}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className={styles.menu} sideOffset={-4}><DropdownMenu.Item className={styles.menuItem} onSelect={() => onUpdate({ scope: 'personal', teamId: '' })}><LockKeyhole/>Personal</DropdownMenu.Item><DropdownMenu.Item className={styles.menuItem} onSelect={() => onUpdate({ scope: 'workspace', teamId: '' })}><Building2/>Workspace</DropdownMenu.Item>{teams.length > 0 && <DropdownMenu.Separator className={styles.separator}/>} {teams.map(team => <DropdownMenu.Item className={styles.menuItem} key={team.id} onSelect={() => onUpdate({ scope: 'team', teamId: team.id })}><TeamIcon team={team}/><span data-i18n-ignore>{team.name}</span></DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    <DropdownMenu.Separator className={styles.separator}/>
    {onSetSubscriptionEvents && <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}><Bell/><span>Subscribe</span><ChevronRight className={styles.trailing}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className={`${styles.menu} ${styles.subscriptionMenu}`} sideOffset={-4}><SubscriptionItem checked={events.has('issue-added')} label={`${entity} is added to the view`} onSelect={() => toggleEvent('issue-added')}/><SubscriptionItem checked={events.has('issue-completed')} label={`${entity} is marked completed or canceled`} onSelect={() => toggleEvent('issue-completed')}/></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>}
    {onShare && <DropdownMenu.Item className={styles.menuItem} onSelect={onShare}><Copy/><span>{view.shareToken ? 'Disable public link' : 'Share view'}</span></DropdownMenu.Item>}
    {(onCopy || onExport) && <DropdownMenu.Separator className={styles.separator}/>}
    {onCopy && <DropdownMenu.Item className={styles.menuItem} onSelect={onCopy}><Copy/>Copy link</DropdownMenu.Item>}
    {onExport && <DropdownMenu.Item className={styles.menuItem} onSelect={onExport}><Download/><span>{`Export ${view.resource === 'projects' ? 'projects' : 'issues'} as CSV…`}</span></DropdownMenu.Item>}
    <DropdownMenu.Separator className={styles.separator}/>
    <DropdownMenu.Item className={styles.menuItem} data-danger onSelect={onDelete}><Trash2/>Delete</DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function SubscriptionItem({ checked, label, onSelect }: { checked: boolean; label: string; onSelect: () => void }) { return <DropdownMenu.CheckboxItem checked={checked} className={styles.menuItem} onSelect={event => { event.preventDefault(); onSelect() }}><span className={styles.checkbox}>{checked && <CheckboxMark/>}</span><span>{label}</span></DropdownMenu.CheckboxItem> }
