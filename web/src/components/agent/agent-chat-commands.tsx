import { deleteAgentSession, restoreAgentSession } from '@/lib/api'
import { toast } from '@/lib/toast'
import type { AgentSession } from '@/types/flow'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { useFlowHotkeys } from '@/components/ui/menu-shortcuts'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'

export const RENAME_CHAT_SHORTCUT = '⇧ R'
/** ⌘ ⌫ on macOS, Ctrl Delete elsewhere (the reference app's hint). */
export function deleteChatShortcut() {
  return isMacPlatform() ? '⌘ ⌫' : '⌘ Delete'
}

/** ⇧R renames and Ctrl Delete (⌘⌫) deletes the open chat, unless focus is in a text field or a menu or dialog is open; `scope` limits it to events from inside that selector. */
export function useAgentChatHotkeys(enabled: boolean, onRename: () => void, onDelete: () => void, scope?: string) {
  useFlowHotkeys({ [RENAME_CHAT_SHORTCUT]: onRename, [deleteChatShortcut()]: onDelete }, enabled, scope)
}

/**
 * Deletes a chat at once (no confirmation) and offers Undo, which restores it on the server.
 * `onDeleted` removes it from the UI; `onRestored` puts it back and shows it again.
 */
export function deleteAgentChat(session: AgentSession, { t, onDeleted, onRestored }: {
  t: (text: string) => string
  onDeleted: () => void
  onRestored: (session: AgentSession) => void
}) {
  return deleteAgentSession(session.id).then(
    () => {
      onDeleted()
      toast.info(t('Chat deleted'), {
        icon: <FlowGlyph name="delete" size={16}/>,
        button: {
          label: t('Undo'),
          onClick: () => void restoreAgentSession(session.id).then(onRestored, reason => toast.error(reason instanceof Error ? reason.message : t('Could not restore chat'))),
        },
      })
    },
    reason => { toast.error(reason instanceof Error ? reason.message : t('Could not delete chat')) },
  )
}
