import { toast } from '@/lib/toast'

/** Copies a message the way the reference app does: the text on the clipboard and a "Copied message" notice. */
export function copyAgentMessage(content: string, t: (source: string) => string) {
  void navigator.clipboard?.writeText(content).then(
    () => toast.info(t('Copied message'), { description: t('Message copied to clipboard') }),
    () => toast.error(t('Could not copy to clipboard')),
  )
}

