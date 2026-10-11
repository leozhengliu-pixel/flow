/**
 * The reference app's confirmation dialog for the template editors and lists ("Discard changes?", "Delete the template …?"):
 * a 480px card with a 15px title and description and Cancel / destructive buttons, the destructive one focused.
 * `confirmTemplateAction` mounts it on demand and resolves with the choice.
 */
import { createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { TemplateConfirmDialog, type TemplateConfirmOptions } from './template-confirm-dialog'

export type { TemplateConfirmOptions }

export function confirmTemplateAction(options: TemplateConfirmOptions): Promise<boolean> {
  return new Promise(resolve => {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    root.render(createElement(TemplateConfirmDialog, {
      options,
      onDone: (confirmed: boolean) => {
        resolve(confirmed)
        // Let the close render before unmounting.
        window.setTimeout(() => { root.unmount(); host.remove() }, 0)
      },
    }))
  })
}
