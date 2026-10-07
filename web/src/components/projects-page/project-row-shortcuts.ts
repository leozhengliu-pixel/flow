import { useLinearRowShortcuts } from '@/components/ui/menu-shortcuts'

/** Key hints on the project row menu, in Linear's order; each also works from a hovered row. */
export const PROJECT_ROW_SHORTCUTS = ['P then S', 'P then P', 'P then A', 'P then M', 'Ctrl ⌥ S', 'Ctrl ⌥ D', 'P then L', '⌥ F', '⇧ H', 'N then C'] as const

/** Opens a hovered or focused project row's menu when one of its hints is pressed. */
export function useProjectRowShortcuts(enabled = true) { useLinearRowShortcuts(PROJECT_ROW_SHORTCUTS, enabled) }
