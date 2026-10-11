/** The reference app's confirmation card used by the template editors and lists; opened through `confirmTemplateAction`. */
import * as Dialog from '@radix-ui/react-dialog'
import { useState } from 'react'
import './issue-template-editor.css'

export interface TemplateConfirmOptions {
  title: string
  description?: string
  confirmLabel: string
  cancelLabel: string
}

export function TemplateConfirmDialog({ options, onDone }: { options: TemplateConfirmOptions; onDone: (confirmed: boolean) => void }) {
  const [open, setOpen] = useState(true)
  const finish = (confirmed: boolean) => { setOpen(false); onDone(confirmed) }
  return <Dialog.Root open={open} onOpenChange={next => { if (!next) finish(false) }}>
    <Dialog.Portal>
      <Dialog.Overlay data-flow-motion="backdrop" className="tpl-dialog-overlay"/>
      <Dialog.Content data-flow-motion="dialog" className="tpl-confirm-dialog" aria-describedby={options.description ? 'tpl-confirm-description' : undefined} onOpenAutoFocus={event => { event.preventDefault(); (event.currentTarget as HTMLElement).querySelector<HTMLButtonElement>('.tpl-danger-button')?.focus() }}>
        <Dialog.Title className="tpl-confirm-title">{options.title}</Dialog.Title>
        {options.description && <Dialog.Description id="tpl-confirm-description" className="tpl-confirm-description">{options.description}</Dialog.Description>}
        <footer>
          <button type="button" className="tpl-confirm-cancel" onClick={() => finish(false)}>{options.cancelLabel}</button>
          <button type="button" className="tpl-danger-button" onClick={() => finish(true)}>{options.confirmLabel}</button>
        </footer>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}
