import * as ContextMenu from '@radix-ui/react-context-menu'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check } from 'lucide-react'
import { createContext, useContext, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import { useI18n } from '@/i18n/i18n'
import { AppLink } from './app-link'
import { createShortcutMatcher, shortcutSteps, takeQueuedShortcut } from './menu-shortcuts'
import './row-context-menu.css'

/**
 * Linear's row context menu (right-click or "…" on a project / initiative row), built on Radix
 * ContextMenu. Every row shares one layout — icon slot, label (+ muted value), key hint, ▶ marker —
 * and every key hint shown is live: it runs while the menu is open, and from a hovered or focused
 * row through `useLinearRowShortcuts`. The same rows render inside a Radix DropdownMenu ("…" buttons)
 * through `LinearDropdownMenuContent`.
 */
export const LinearContextMenuRoot = ContextMenu.Root
export const LinearContextMenuTrigger = ContextMenu.Trigger
export const LinearContextMenuPortal = ContextMenu.Portal

/** Radix ContextMenu and DropdownMenu expose the same menu parts; rows use whichever surface they sit in. */
type MenuKit = Pick<typeof ContextMenu, 'Item' | 'CheckboxItem' | 'Separator' | 'Sub' | 'SubTrigger' | 'SubContent' | 'Portal'>
const KitContext = createContext<MenuKit>(ContextMenu)
const DROPDOWN_KIT = DropdownMenu as unknown as MenuKit

type ShortcutEntry = { shortcut: string; run: () => void }
const RegistryContext = createContext<Set<ShortcutEntry> | null>(null)

function useRegisterShortcut(shortcut: string | undefined, run: () => void, disabled = false) {
  const registry = useContext(RegistryContext)
  const runRef = useRef(run)
  useEffect(() => { runRef.current = run })
  useEffect(() => {
    if (!registry || !shortcut || disabled) return
    const entry = { shortcut, run: () => runRef.current() }
    registry.add(entry)
    return () => { registry.delete(entry) }
  }, [registry, shortcut, disabled])
}

/** Runs registered hints for key presses inside this menu surface (not its submenus or inputs). */
function useShortcutKeys(registry: Set<ShortcutEntry>) {
  const matcher = useMemo(() => createShortcutMatcher(isMacPlatform()), [])
  return (event: KeyboardEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement
    if (target.closest('input,textarea,[contenteditable="true"]') || target.closest('[role=menu]') !== event.currentTarget) return
    const result = matcher.match(event.nativeEvent, [...registry].map(entry => entry.shortcut))
    if (!result) return
    // Stops Radix typeahead from jumping to a row that starts with the same letter.
    event.preventDefault()
    event.stopPropagation()
    if (result !== 'pending') [...registry].find(entry => entry.shortcut === result)?.run()
  }
}

function useQueuedShortcut(registry: Set<ShortcutEntry>) {
  useEffect(() => {
    const shortcut = takeQueuedShortcut()
    if (shortcut) [...registry].find(entry => entry.shortcut === shortcut)?.run()
  }, [registry])
}

type ContentProps = { label: string; children: ReactNode; className?: string; onCloseAutoFocus?: (event: Event) => void }

export function LinearMenuContent({ label, children, className = '', onCloseAutoFocus }: ContentProps) {
  const registry = useMemo(() => new Set<ShortcutEntry>(), [])
  const onKeyDown = useShortcutKeys(registry)
  useQueuedShortcut(registry)
  return <RegistryContext.Provider value={registry}>
    <ContextMenu.Content data-flow-motion="floating" aria-label={label} className={`linear-menu ${className}`} collisionPadding={8} onCloseAutoFocus={onCloseAutoFocus} onKeyDown={onKeyDown}>
      {children}
    </ContextMenu.Content>
  </RegistryContext.Provider>
}

/** The same menu as a Radix DropdownMenu surface (a header "…" button). */
export function LinearDropdownMenuContent({ label, children, className = '', onCloseAutoFocus, align = 'start' }: ContentProps & { align?: 'start' | 'center' | 'end' }) {
  const registry = useMemo(() => new Set<ShortcutEntry>(), [])
  const onKeyDown = useShortcutKeys(registry)
  useQueuedShortcut(registry)
  return <KitContext.Provider value={DROPDOWN_KIT}>
    <RegistryContext.Provider value={registry}>
      <DropdownMenu.Content data-flow-motion="floating" aria-label={label} align={align} sideOffset={4} className={`linear-menu ${className}`} collisionPadding={8} onCloseAutoFocus={onCloseAutoFocus} onKeyDown={onKeyDown}>
        {children}
      </DropdownMenu.Content>
    </RegistryContext.Provider>
  </KitContext.Provider>
}

export function LinearMenuShortcut({ value }: { value: string }) {
  const { t } = useI18n()
  const mac = isMacPlatform()
  const steps = shortcutSteps(value, mac)
  return <span className="linear-menu__shortcut" aria-hidden="true" data-shortcut={value}>
    {steps.map((keys, index) => <span key={index} style={{ display: 'contents' }}>
      {index > 0 && <span>{t('then')}</span>}
      {keys.map((key, keyIndex) => <kbd key={keyIndex}>{key}</kbd>)}
    </span>)}
  </span>
}

type RowProps = { icon?: ReactNode; label: ReactNode; detail?: ReactNode; shortcut?: string; trailing?: ReactNode; submenu?: boolean; leading?: ReactNode }
/** The shared row layout; every row is left-aligned (icon, label, then hint and marker at the end). */
export function LinearMenuRowContent({ icon, label, detail, shortcut, trailing, submenu, leading }: RowProps) {
  return <>
    {leading}
    {icon !== undefined && <span className="linear-menu__icon" aria-hidden="true">{icon}</span>}
    <span className="linear-menu__label"><span className="linear-menu__text">{label}</span>{detail && <span className="linear-menu__detail">{detail}</span>}{trailing}</span>
    {(shortcut || submenu) && <span className="linear-menu__end">{shortcut && <LinearMenuShortcut value={shortcut}/>}{submenu && <span className="linear-menu__marker" aria-hidden="true">▶</span>}</span>}
  </>
}

function useLabel(label: string, translate: boolean) {
  const { t } = useI18n()
  return translate ? t(label) : label
}

/** A menu row; with `href` it is an in-app link (e.g. a settings page) like the saved-view menu's. */
export function LinearMenuItem({ icon, label, detail, shortcut, onSelect, href, disabled = false, translate = true, keepOpen = false }: { icon?: ReactNode; label: string; detail?: ReactNode; shortcut?: string; onSelect?: () => void; href?: string; disabled?: boolean; translate?: boolean; keepOpen?: boolean }) {
  const Kit = useContext(KitContext)
  const ref = useRef<HTMLDivElement>(null)
  const text = useLabel(label, translate)
  useRegisterShortcut(shortcut, () => ref.current?.click(), disabled)
  const content = <LinearMenuRowContent icon={icon} label={text} detail={detail} shortcut={shortcut}/>
  return <Kit.Item ref={ref} asChild={Boolean(href)} className="linear-menu__row" data-menu-item={label} disabled={disabled} onSelect={event => { if (keepOpen) event.preventDefault(); onSelect?.() }} data-i18n-ignore={translate ? undefined : ''}>
    {href ? <AppLink href={href}>{content}</AppLink> : content}
  </Kit.Item>
}

export function LinearMenuSeparator() {
  const Kit = useContext(KitContext)
  return <Kit.Separator className="linear-menu__separator"/>
}

export type LinearSubmenuApi = { close: () => void; container: HTMLElement | null }

/** A row that opens a nested menu; the nested menu's first row lines up with the trigger row. */
export function LinearSubmenu({ icon, label, detail, shortcut, children, className = '', search = false, disabled = false, translate = true, onOpenChange }: { icon?: ReactNode; label: string; detail?: ReactNode; shortcut?: string; children: ReactNode | ((api: LinearSubmenuApi) => ReactNode); className?: string; search?: boolean; disabled?: boolean; translate?: boolean; onOpenChange?: (open: boolean) => void }) {
  const Kit = useContext(KitContext)
  const [open, setOpenState] = useState(false)
  const setOpen = (next: boolean) => { setOpenState(next); onOpenChange?.(next) }
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  const trigger = useRef<HTMLDivElement>(null)
  const text = useLabel(label, translate)
  const registry = useMemo(() => new Set<ShortcutEntry>(), [])
  const onKeyDown = useShortcutKeys(registry)
  useRegisterShortcut(shortcut, () => setOpen(true), disabled)
  const close = () => { setOpen(false); trigger.current?.focus() }
  // A submenu with a filter field takes focus there, like Linear's pickers.
  useEffect(() => {
    if (!container) return
    const frame = requestAnimationFrame(() => container.querySelector<HTMLInputElement>('.linear-menu__search input, input')?.focus({ preventScroll: true }))
    return () => cancelAnimationFrame(frame)
  }, [container])
  return <Kit.Sub open={open} onOpenChange={setOpen}>
    <Kit.SubTrigger ref={trigger} className="linear-menu__row" data-menu-item={label} disabled={disabled}>
      <LinearMenuRowContent icon={icon} label={text} detail={detail} shortcut={shortcut} submenu/>
    </Kit.SubTrigger>
    <Kit.Portal>
      <RegistryContext.Provider value={registry}>
        <Kit.SubContent ref={setContainer} data-flow-motion="floating" aria-label={text} className={`linear-menu linear-menu--sub${search ? ' has-search' : ''} ${className}`} data-menu={label} sideOffset={-2} alignOffset={search ? -42.3 : -6.5} collisionPadding={8}
          onFocusOutside={event => {
            // Radix briefly focuses the parent menu while the pointer crosses into the submenu.
            const target = event.target
            if (target instanceof HTMLElement && target.matches('[role=menu]') && target.contains(trigger.current)) event.preventDefault()
          }}
          onKeyDown={onKeyDown}>
          {typeof children === 'function' ? children({ close, container }) : children}
        </Kit.SubContent>
      </RegistryContext.Provider>
    </Kit.Portal>
  </Kit.Sub>
}

/** Filter field at the top of a submenu (Linear: "Change status…", "Set lead…", "Filter…"). */
export function LinearMenuSearch({ value, onChange, placeholder, onChoose, translate = true }: { value: string; onChange: (value: string) => void; placeholder: string; onChoose?: (key: string) => boolean; translate?: boolean }) {
  const text = useLabel(placeholder, translate)
  return <div className="linear-menu__search">
    <input aria-label={text} autoComplete="off" placeholder={text} spellCheck={false} value={value} onChange={event => onChange(event.target.value)} onKeyDown={event => {
      if (event.key === 'Escape' || (event.key === 'ArrowLeft' && !value)) return
      event.stopPropagation()
      if (!value && onChoose?.(event.key)) { event.preventDefault(); return }
      if (event.key !== 'ArrowDown' && event.key !== 'Enter') return
      const first = event.currentTarget.closest('[role=menu]')?.querySelector<HTMLElement>('[role^=menuitem]:not([data-disabled])')
      if (!first) return
      event.preventDefault()
      if (event.key === 'Enter') first.click()
      else first.focus()
    }}/>
  </div>
}

export type LinearMenuOption = { id: string; label: string; icon?: ReactNode; detail?: ReactNode; shortcut?: string; keywords?: string; disabled?: boolean; translate?: boolean; group?: string }

/**
 * The rows of a picker submenu with an optional filter field. Single-select rows show Linear's
 * check after the label and their number key; multi-select rows lead with a checkbox.
 */
export function LinearMenuOptions({ options, selected, multiple = false, placeholder, onChoose, keepOpen = multiple, emptyLabel = 'No results', matches, footer }: { options: LinearMenuOption[]; selected: ReadonlySet<string>; multiple?: boolean; placeholder?: string; onChoose: (id: string) => void; keepOpen?: boolean; emptyLabel?: string; matches?: (option: LinearMenuOption, query: string) => boolean; footer?: (query: string) => ReactNode }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const normalized = query.trim().toLocaleLowerCase()
  const visible = normalized ? options.filter(option => matches ? matches(option, normalized) : `${option.label} ${option.translate === false ? '' : t(option.label)} ${option.keywords ?? ''}`.toLocaleLowerCase().includes(normalized)) : options
  const list = useRef<HTMLDivElement>(null)
  // A row's number key picks it from the filter field too; clicking the row closes the menu like Linear.
  const byKey = (key: string) => {
    const option = options.find(item => item.shortcut === key && !item.disabled)
    const row = option && [...list.current?.querySelectorAll<HTMLElement>('[data-option-id]') ?? []].find(item => item.dataset.optionId === option.id)
    if (!row) return false
    row.click()
    return true
  }
  return <>
    {placeholder && <LinearMenuSearch value={query} onChange={setQuery} placeholder={placeholder} onChoose={byKey}/>}
    <div className={placeholder ? 'linear-menu__list' : undefined} ref={list} role="presentation">
      {visible.map((option, index) => <OptionRow key={option.id || '__none'} option={option} group={option.group && option.group !== visible[index - 1]?.group ? option.group : undefined} checked={selected.has(option.id)} multiple={multiple} keepOpen={keepOpen} onChoose={onChoose}/>)}
      {footer?.(query.trim())}
      {!visible.length && !footer?.(query.trim()) && <div className="linear-menu__empty">{t(emptyLabel)}</div>}
    </div>
  </>
}

function OptionRow({ option, group, checked, multiple, keepOpen, onChoose }: { option: LinearMenuOption; group?: string; checked: boolean; multiple: boolean; keepOpen: boolean; onChoose: (id: string) => void }) {
  const { t } = useI18n()
  const label = option.translate === false ? option.label : t(option.label)
  const Kit = useContext(KitContext)
  const ref = useRef<HTMLDivElement>(null)
  useRegisterShortcut(option.shortcut, () => ref.current?.click(), option.disabled)
  const content = multiple
    ? <LinearMenuRowContent leading={<span className="linear-menu__checkbox" aria-hidden="true"><span>{checked && <Check strokeWidth={3}/>}</span></span>} icon={option.icon} label={label} detail={option.detail} shortcut={option.shortcut}/>
    : <LinearMenuRowContent icon={option.icon} label={label} detail={option.detail} shortcut={option.shortcut} trailing={checked ? <span className="linear-menu__check" aria-hidden="true"><Check size={14}/></span> : undefined}/>
  const i18n = option.translate === false ? '' : undefined
  return <>
    {group && <div className="linear-menu__group-label" data-i18n-ignore={i18n}>{option.translate === false ? group : t(group)}</div>}
    {multiple
      ? <Kit.CheckboxItem ref={ref} className="linear-menu__row" data-option-id={option.id} checked={checked} disabled={option.disabled} data-i18n-ignore={i18n} onSelect={event => { if (keepOpen) event.preventDefault() }} onCheckedChange={() => onChoose(option.id)}>{content}</Kit.CheckboxItem>
      : <Kit.Item ref={ref} className="linear-menu__row" data-option-id={option.id} aria-checked={checked} disabled={option.disabled} data-i18n-ignore={i18n} onSelect={event => { if (keepOpen) event.preventDefault(); onChoose(option.id) }}>{content}</Kit.Item>}
  </>
}
