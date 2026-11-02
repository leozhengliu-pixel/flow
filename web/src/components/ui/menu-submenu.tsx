import * as ContextMenu from '@radix-ui/react-context-menu'
import { ChevronLeft } from 'lucide-react'
import { useCallback, useState, type ComponentProps, type ReactNode } from 'react'
import { useOptionalTranslate } from '@/i18n/i18n'
import { usePhoneSubmenuLevel } from './use-phone-submenu-level'
import './menu-submenu.css'

export type SubmenuKit = Pick<typeof ContextMenu, 'Sub' | 'SubTrigger' | 'SubContent' | 'Portal'>

type SubmenuRef = { ref?: (node: HTMLDivElement | null) => void }

export function PhoneSubmenuBack({ label, onBack }: { label: string; onBack: () => void }) {
  const t = useOptionalTranslate()
  return <button type="button" className="menu-submenu-back" aria-label={t('Back')} onClick={onBack}><ChevronLeft size={16}/><span>{label}</span></button>
}

/** Keeps the anchored desktop cascade and replaces only the current level on phones. */
export function MenuSubmenu({ kit: Kit = ContextMenu, label, trigger, children, triggerProps, contentProps, open: controlledOpen, onOpenChange }: {
  kit?: SubmenuKit
  label: string
  trigger: ReactNode
  children: ReactNode
  triggerProps?: Omit<ComponentProps<typeof ContextMenu.SubTrigger>, 'ref'> & SubmenuRef & { 'data-menu-item'?: string }
  contentProps?: Omit<ComponentProps<typeof ContextMenu.SubContent>, 'ref'> & SubmenuRef & { 'data-flow-motion'?: string; 'data-menu'?: string }
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const [localOpen, setLocalOpen] = useState(false)
  const [triggerNode, setTriggerNode] = useState<HTMLDivElement | null>(null)
  const [contentNode, setContentNode] = useState<HTMLDivElement | null>(null)
  const triggerRef = triggerProps?.ref
  const setTrigger = useCallback((node: HTMLDivElement | null) => {
    setTriggerNode(node)
    triggerRef?.(node)
  }, [triggerRef])
  const contentRef = contentProps?.ref
  const setContent = useCallback((node: HTMLDivElement | null) => {
    setContentNode(node)
    contentRef?.(node)
  }, [contentRef])
  const open = controlledOpen ?? localOpen
  const phone = usePhoneSubmenuLevel(open, triggerNode, contentNode)
  const setOpen = (next: boolean) => { setLocalOpen(next); onOpenChange?.(next) }
  const back = () => {
    setOpen(false)
    requestAnimationFrame(() => triggerNode?.focus({ preventScroll: true }))
  }
  return <Kit.Sub open={open} onOpenChange={setOpen}>
    <Kit.SubTrigger {...triggerProps} ref={setTrigger}>{trigger}</Kit.SubTrigger>
    <Kit.Portal>
      <Kit.SubContent {...contentProps} ref={setContent} {...(phone ? { 'aria-label': label, 'aria-labelledby': undefined } : {})} data-phone-submenu={phone ? '' : undefined}
        onFocusOutside={event => {
          const target = event.detail.originalEvent.target
          if (phone && target instanceof Element && target.closest('[data-phone-submenu]')) event.preventDefault()
          contentProps?.onFocusOutside?.(event)
        }}>
        {phone && <PhoneSubmenuBack label={label} onBack={back}/>}
        {children}
      </Kit.SubContent>
    </Kit.Portal>
  </Kit.Sub>
}
