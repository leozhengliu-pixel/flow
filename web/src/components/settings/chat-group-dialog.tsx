import * as RadixDialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { ReactNode } from 'react'

import { Dialog, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { chatProviderName } from '@/lib/integration-catalog'
import type { ChatIntegrationProvider } from '@/types/chat-integrations'

import { ChatGroupDialogContext } from './chat-group-dialog-context'
import { IntegrationBrandIcon } from './integration-brand-icon'
import './feature-settings.css'
import './chat-integration-settings.css'
import './chat-group-dialog.css'

/**
 * The dialog that connects a project's or initiative's Lark/Feishu groups.
 * It is a panel with the proportions of the project notification panel (see
 * chat-group-dialog.css): the title and description stay put while the form
 * scrolls, the notification options are that panel's checkbox rows, and the
 * Flow-only rows keep the settings controls (select and switch).
 */
export function ChatGroupDialog({ open, onOpenChange, provider, subject, description, className = '', children }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  provider: ChatIntegrationProvider
  /** The project or initiative the groups belong to. */
  subject: string
  description: string
  className?: string
  children: ReactNode
}) {
  const { t } = useI18n()
  const name = chatProviderName(provider, t)
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <RadixDialog.Portal>
      <RadixDialog.Overlay data-flow-motion="backdrop" className="dialog-overlay"/>
      <RadixDialog.Content data-flow-motion="dialog" data-tooltip-raised="" className={`chat-group-dialog${className ? ` ${className}` : ''}`}>
        <header className="chat-group-dialog__header">
          <DialogTitle><IntegrationBrandIcon provider={provider} size={16}/><span>{t('{name} notifications for').replace('{name}', () => name)} <span data-i18n-ignore>{subject}</span></span></DialogTitle>
          <RadixDialog.Description>{description}</RadixDialog.Description>
        </header>
        <ChatGroupDialogContext.Provider value><div className="chat-group-dialog__body">{children}</div></ChatGroupDialogContext.Provider>
        <RadixDialog.Close className="dialog-close" aria-label={t('Close')}><X size={15}/></RadixDialog.Close>
      </RadixDialog.Content>
    </RadixDialog.Portal>
  </Dialog>
}
