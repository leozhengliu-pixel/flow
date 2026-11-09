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
 * The dialog that connects a project's or initiative's Lark/Feishu groups. It has the proportions of the reference app's
 * project Slack window (see chat-group-dialog.css): a wide dialog whose header names the chat app and the subject, a one-line
 * description, a divider, and then rows with the label on the left and the control on the right.
 */
export function ChatGroupDialog({ open, onOpenChange, provider, subject, subjectIcon, description, className = '', children }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  provider: ChatIntegrationProvider
  /** The project or initiative the groups belong to. */
  subject: string
  /** Its icon, shown before the name. */
  subjectIcon?: ReactNode
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
          <DialogTitle><IntegrationBrandIcon provider={provider} size={18}/><span>{t('{name} notifications for').replace('{name}', () => name)}</span>{subjectIcon}<span className="chat-group-dialog__subject" data-i18n-ignore>{subject}</span></DialogTitle>
          <RadixDialog.Close className="chat-group-dialog__close" aria-label={t('Close')}><X size={18}/></RadixDialog.Close>
        </header>
        <RadixDialog.Description className="chat-group-dialog__description">{description}</RadixDialog.Description>
        <ChatGroupDialogContext.Provider value><div className="chat-group-dialog__body">{children}</div></ChatGroupDialogContext.Provider>
      </RadixDialog.Content>
    </RadixDialog.Portal>
  </Dialog>
}
