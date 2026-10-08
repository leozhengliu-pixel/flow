import * as Dialog from '@radix-ui/react-dialog'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, CustomerRequest } from '@/types/flow'
import { EmbeddedCustomerNeedForm } from './embedded-customer-need-form'
import './customer-request-create-dialog.css'

/**
 * "Add customer request…" where the issue or project page is not open (Linear's customer need
 * create modal): the composer in a dialog.
 */
export function CustomerRequestCreateDialog({ open, onOpenChange, data, issueId, projectId, onCreated }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  data: BootstrapData
  issueId?: string
  projectId?: string
  onCreated?: (request: CustomerRequest) => void
}) {
  const { t } = useI18n()
  return <Dialog.Root open={open} onOpenChange={onOpenChange}>
    <Dialog.Portal>
      <Dialog.Overlay data-flow-motion="backdrop" className="dialog-overlay"/>
      <Dialog.Content data-flow-motion="dialog" className="customer-request-create-dialog" aria-describedby={undefined} onEscapeKeyDown={event => event.preventDefault()}>
        <Dialog.Title className="sr-only">{t('Add customer request')}</Dialog.Title>
        {open && <EmbeddedCustomerNeedForm
          data={data}
          host={issueId ? 'issuePage' : 'projectPage'}
          issueId={issueId}
          projectId={projectId}
          onCancel={() => onOpenChange(false)}
          onCreated={request => { onCreated?.(request); onOpenChange(false) }}
        />}
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}
