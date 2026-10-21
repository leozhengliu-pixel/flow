import type { User } from '@/types/flow'
import { confirmAction } from '@/components/ui/action-dialog-service'

/** The reference app `bB`: after unassigning, ask whether the delegated agent should go too ("Keep agent" leaves it). */
export async function confirmRemoveDelegate(delegate: User, sessionActive: boolean, translate: (source: string) => string) {
  const name = delegate.displayName || delegate.name
  const description = sessionActive
    ? translate('{name} is still set to work on this issue. You can keep the agent on the issue, or remove it and archive its working session.')
    : translate('{name} is still set to work on this issue. You can keep the agent on the issue.')
  return confirmAction(translate('Remove {name} too?').replace('{name}', name), { description: description.replace('{name}', name), confirmLabel: translate('Remove agent'), cancelLabel: translate('Keep agent'), danger: false })
}
