import * as Dialog from '@radix-ui/react-dialog'
import { Command } from 'cmdk'
import { useRef, type ReactNode } from 'react'
import { useI18n } from '@/i18n/i18n'

export function PhoneMenu({ open, onOpenChange, label, children, className = '', heading = false, onCloseAutoFocus }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  label: string
  children: ReactNode
  className?: string
  heading?: boolean | string
  onCloseAutoFocus?: Dialog.DialogContentProps['onCloseAutoFocus']
}) {
  const previousFocus = useRef<HTMLElement | null>(null)
  return <Dialog.Root open={open} onOpenChange={onOpenChange}>
    <Dialog.Portal>
      <Dialog.Overlay data-flow-motion="backdrop" className="phone-menu-overlay"/>
      <Dialog.Content data-flow-motion="dialog" data-dialog-placement="window" className={`phone-menu-surface ${className}`} aria-describedby={undefined}
        onOpenAutoFocus={() => { previousFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null }}
        onCloseAutoFocus={event => {
          onCloseAutoFocus?.(event)
          if (!event.defaultPrevented && previousFocus.current?.isConnected) {
            event.preventDefault()
            previousFocus.current.focus({ preventScroll: true })
          }
        }}>
        <Dialog.Title className="sr-only">{label}</Dialog.Title>
        {heading && <div className="phone-menu-heading"><span className={typeof heading === 'string' ? 'phone-menu-context' : undefined}>{typeof heading === 'string' ? heading : label}</span></div>}
        {children}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}

export function PhoneCommandMenu({ children, placeholder = 'Type a command or search...', ...props }: Parameters<typeof PhoneMenu>[0] & { placeholder?: string }) {
  const { t } = useI18n()
  return <PhoneMenu {...props}><Command label={props.label}>
    <div className="phone-menu-search"><Command.Input aria-label={t('Command menu')} placeholder={t(placeholder)}/></div>
    <Command.List>{children}</Command.List>
  </Command></PhoneMenu>
}
