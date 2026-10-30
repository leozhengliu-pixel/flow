import * as P from '@radix-ui/react-dropdown-menu'
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { CheckboxMark } from './checkbox-mark'
import { createContext, useContext, useState, type ComponentProps, type ReactNode } from 'react'
import { Slot } from '@radix-ui/react-slot'
import { Command } from 'cmdk'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { useOptionalTranslate } from '@/i18n/i18n'
import { PhoneCommandMenu } from './phone-menu'

type PhoneMenuState = { open: boolean; setOpen: (open: boolean) => void; close: () => void }
const PhoneMenuContext = createContext<PhoneMenuState | null>(null)

export function DropdownMenu({ children, open: controlledOpen, onOpenChange, defaultOpen, phoneSheet = true, ...props }: ComponentProps<typeof P.Root> & { phoneSheet?: boolean }) {
  const phone = usePhoneLayout()
  const [localOpen, setLocalOpen] = useState(defaultOpen ?? false)
  const open = controlledOpen ?? localOpen
  const setOpen = (next: boolean) => { setLocalOpen(next); onOpenChange?.(next) }
  const sheet = phone && phoneSheet
  return <PhoneMenuContext.Provider value={sheet ? { open, setOpen, close: () => setOpen(false) } : null}>
    {sheet ? children : <P.Root open={open} onOpenChange={setOpen} {...props}>{children}</P.Root>}
  </PhoneMenuContext.Provider>
}

export function DropdownMenuTrigger({ asChild, onClick, ...props }: P.DropdownMenuTriggerProps) {
  const menu = useContext(PhoneMenuContext)
  if (!menu) return <P.Trigger asChild={asChild} onClick={onClick} {...props}/>
  const Trigger = asChild ? Slot : 'button'
  return <Trigger type="button" aria-haspopup="dialog" aria-expanded={menu.open} data-state={menu.open ? 'open' : 'closed'} {...props} onClick={event => { onClick?.(event); if (!event.defaultPrevented) menu.setOpen(!menu.open) }}/>
}

export function DropdownMenuContent({ className, sideOffset = 6, children, ...props }: P.DropdownMenuContentProps) {
  const menu = useContext(PhoneMenuContext)
  if (menu) return <PhoneDropdownMenuContent menu={menu} label={props['aria-label']} onCloseAutoFocus={props.onCloseAutoFocus}>{children}</PhoneDropdownMenuContent>
  return <P.Portal><P.Content data-flow-motion="floating" sideOffset={sideOffset} className={cn('menu-content',className)} {...props}>{children}</P.Content></P.Portal>
}

// The translator hook lives in the phone-only branch so desktop menus keep
// working under the minimal i18n mocks used by isolated unit tests.
function PhoneDropdownMenuContent({ menu, label, onCloseAutoFocus, children }: { menu: PhoneMenuState; label?: string; onCloseAutoFocus?: P.DropdownMenuContentProps['onCloseAutoFocus']; children: ReactNode }) {
  const t = useOptionalTranslate()
  return <PhoneCommandMenu open={menu.open} onOpenChange={menu.setOpen} onCloseAutoFocus={onCloseAutoFocus} label={label ?? t('Command menu')} placeholder="Filter...">{children}</PhoneCommandMenu>
}

export function DropdownMenuItem({ className, onSelect, disabled, children, asChild, textValue, ...props }: P.DropdownMenuItemProps) {
  const menu = useContext(PhoneMenuContext)
  if (menu) return <Command.Item {...props} className={cn('menu-item', className)} value={textValue} disabled={disabled} asChild={asChild} onSelect={() => {
    const event = new Event('select', { cancelable: true })
    onSelect?.(event)
    if (!event.defaultPrevented) menu.close()
  }}>{children}</Command.Item>
  return <P.Item className={cn('menu-item',className)} textValue={textValue} onSelect={onSelect} disabled={disabled} asChild={asChild} {...props}>{children}</P.Item>
}

export function DropdownMenuCheckboxItem({ children, checked, className, onCheckedChange, onSelect, disabled, ...props }: P.DropdownMenuCheckboxItemProps) {
  const menu = useContext(PhoneMenuContext)
  if (menu) return <Command.Item className={cn('menu-item', className)} aria-checked={checked === 'indeterminate' ? 'mixed' : checked} disabled={disabled} onSelect={() => {
    const event = new Event('select', { cancelable: true })
    onSelect?.(event)
    onCheckedChange?.(checked !== true)
    if (!event.defaultPrevented) menu.close()
  }}><span className="phone-menu-check">{checked === true && <CheckboxMark/>}</span>{children}</Command.Item>
  return <P.CheckboxItem className={cn('menu-item',className)} checked={checked} onCheckedChange={onCheckedChange} onSelect={onSelect} disabled={disabled} {...props}><span className="menu-indicator"><P.ItemIndicator><CheckboxMark/></P.ItemIndicator></span>{children}</P.CheckboxItem>
}

export function DropdownMenuSeparator(props: P.DropdownMenuSeparatorProps) {
  return useContext(PhoneMenuContext) ? <Command.Separator/> : <P.Separator className="menu-separator" {...props}/>
}

export function DropdownMenuLabel({ className, children, ...props }: P.DropdownMenuLabelProps) {
  return useContext(PhoneMenuContext) ? <div cmdk-group-heading="">{children}</div> : <P.Label className={cn('menu-label',className)} {...props}>{children}</P.Label>
}

export function DropdownMenuSub({ children, open: controlledOpen, onOpenChange, defaultOpen }: ComponentProps<typeof P.Sub>) {
  const parent = useContext(PhoneMenuContext)
  const [localOpen, setLocalOpen] = useState(defaultOpen ?? false)
  const open = controlledOpen ?? localOpen
  const setOpen = (next: boolean) => { setLocalOpen(next); onOpenChange?.(next) }
  if (!parent) return <P.Sub open={open} onOpenChange={setOpen}>{children}</P.Sub>
  return <PhoneMenuContext.Provider value={{ open, setOpen, close: () => { setOpen(false); parent.close() } }}>{children}</PhoneMenuContext.Provider>
}

export function DropdownMenuSubTrigger({ children, className, disabled, ...props }: P.DropdownMenuSubTriggerProps) {
  const menu = useContext(PhoneMenuContext)
  if (menu) return <Command.Item disabled={disabled} onSelect={() => menu.setOpen(true)}>{children}<ChevronRight className="phone-menu-current" size={13}/></Command.Item>
  return <P.SubTrigger className={cn('menu-item',className)} disabled={disabled} {...props}>{children}<ChevronRight size={13} className="menu-chevron"/></P.SubTrigger>
}

export function DropdownMenuSubContent({ className, children, onFocusOutside, ...props }: P.DropdownMenuSubContentProps) {
  const menu = useContext(PhoneMenuContext)
  if (menu) return <PhoneDropdownMenuContent menu={menu} label={props['aria-label']}>{children}</PhoneDropdownMenuContent>
  return <P.Portal><P.SubContent data-flow-motion="floating" className={cn('menu-content',className)} onFocusOutside={event => {
    // Nested controls (a calendar) keep focus inside this submenu; that focus move is not a dismissal.
    const target = event.detail.originalEvent.target
    const layer = event.currentTarget
    if (layer instanceof Node && target instanceof Node && layer.contains(target)) event.preventDefault()
    onFocusOutside?.(event)
  }} {...props}>{children}</P.SubContent></P.Portal>
}
