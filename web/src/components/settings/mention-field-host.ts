/**
 * Hosting a MentionTextField inside a Radix modal dialog: the "@" menu is portaled to the document body, outside the
 * dialog's content, so a pointer-down on it must not count as an outside interaction that dismisses the dialog.
 */
type OutsideEvent = Event & { detail?: { originalEvent?: Event } }

export function keepMentionMenuOpen(event: OutsideEvent) {
  const target = (event.detail?.originalEvent?.target ?? event.target) as Element | null
  if (target?.closest?.('.description-mention-menu')) event.preventDefault()
}
