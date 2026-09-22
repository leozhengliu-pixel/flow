import { useCallback, useMemo, useState } from 'react'

import type { BootstrapData, WorkspaceSettings } from '@/types/flow'
import { updateWorkspacePreferences } from '@/lib/api'
import {
  SECURITY_ROLE_OPTIONS,
  isRestrictingSecurityPermission,
  normalizeSecurityPermission,
  securityPermissionLabel,
  type SecuritySettingKey,
} from '@/lib/security-setting'

type SaveFn = (next: WorkspaceSettings) => Promise<void>

/**
 * LS-0764 — security permission hook over flat WorkspaceSettings fields.
 * Options: All members / Only owners and admins / Only owners / Only admins.
 * API key tighten path surfaces a confirm dialog.
 */
export function useSecuritySetting(
  key: SecuritySettingKey,
  settings: WorkspaceSettings,
  save: SaveFn,
  options: { onReload?: () => Promise<void> } = {},
) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [pendingValue, setPendingValue] = useState<string | null>(null)

  const value = normalizeSecurityPermission(
    (settings[key] as string | undefined) ?? (key === 'agentGuidancePermission' ? 'admins' : 'members'),
  )

  const roleOptions = useMemo(() => SECURITY_ROLE_OPTIONS, [])

  const apply = useCallback(
    async (next: string) => {
      const normalized = normalizeSecurityPermission(next)
      await save({ ...settings, [key]: normalized })
      await options.onReload?.()
    },
    [key, options, save, settings],
  )

  const onValueChange = useCallback(
    (next: string) => {
      if (key === 'apiKeyPermission' && isRestrictingSecurityPermission(value, next)) {
        setPendingValue(normalizeSecurityPermission(next))
        setConfirmOpen(true)
        return
      }
      void apply(next)
    },
    [apply, key, value],
  )

  const confirmRestrict = useCallback(async () => {
    if (!pendingValue) return
    setConfirmOpen(false)
    const next = pendingValue
    setPendingValue(null)
    await apply(next)
  }, [apply, pendingValue])

  const cancelRestrict = useCallback(() => {
    setConfirmOpen(false)
    setPendingValue(null)
  }, [])

  const confirmTitle = pendingValue
    ? `Are you sure you want to restrict member API keys to ${securityPermissionLabel(pendingValue).toLowerCase()}?`
    : ''
  const confirmDescription = pendingValue
    ? `This will prevent some workspace members from creating new API keys. Existing API keys will continue to work, and workspace ${securityPermissionLabel(pendingValue).toLowerCase()} will still be able to create API keys.`
    : ''

  return {
    options: roleOptions,
    value,
    label: securityPermissionLabel(value),
    onValueChange,
    confirmOpen,
    confirmTitle,
    confirmDescription,
    confirmRestrict,
    cancelRestrict,
  }
}

/** Convenience saver bound to workspace preferences for settings pages. */
export function saveWorkspaceSecuritySettings(
  data: BootstrapData,
  next: WorkspaceSettings,
): Promise<WorkspaceSettings> {
  return updateWorkspacePreferences(next, data.workspace.urlKey)
}
