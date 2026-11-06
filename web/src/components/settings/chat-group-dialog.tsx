import * as RadixDialog from '@radix-ui/react-dialog'
import type { ReactNode } from 'react'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { chatProviderName } from '@/lib/integration-catalog'
import type { ChatIntegrationProvider } from '@/types/chat-integrations'

import { IntegrationBrandIcon } from './integration-brand-icon'
import './feature-settings.css'
import './chat-integration-settings.css'
import './chat-group-dialog.css'

/**
 * The dialog that connects a project's or initiative's Lark/Feishu groups.
 * It is Flow's shared dialog with the settings dialog skin: the title and
 * description stay put while the form scrolls, and the form itself is the
 * settings form (settings rows, select and switch).
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
    <DialogContent className={`feature-dialog chat-group-dialog${className ? ` ${className}` : ''}`} closeLabel={t('Close')}>
      <header className="chat-group-dialog__header">
        <DialogTitle><IntegrationBrandIcon provider={provider} size={16}/><span>{t('{name} notifications for').replace('{name}', () => name)} <span data-i18n-ignore>{subject}</span></span></DialogTitle>
        <RadixDialog.Description>{description}</RadixDialog.Description>
      </header>
      <div className="chat-group-dialog__body">{children}</div>
    </DialogContent>
  </Dialog>
}
