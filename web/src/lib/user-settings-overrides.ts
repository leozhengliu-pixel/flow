import type { UserSettings } from '@/types/flow'

/**
 * Local user-settings writes that the server snapshot may not reflect yet.
 *
 * A settings PATCH can take a while on a busy workspace, and a workspace
 * bootstrap fetched before it lands still carries the old values. Without
 * this overlay the app re-applies those stale values (e.g. the theme flips
 * back to Light seconds after choosing Dark). Each written field is kept
 * until a server snapshot shows the saved value, the write fails, or a short
 * grace period after it succeeded runs out.
 */
type Override = { value: unknown; settledAt?: number }

export const USER_SETTINGS_OVERRIDES_EVENT = 'flow:user-settings-overrides-changed'
const SETTLED_GRACE_MS = 60_000
const overrides = new Map<string, Map<string, Override>>()

function notify() {
  if (typeof window !== 'undefined') window.dispatchEvent(new CustomEvent(USER_SETTINGS_OVERRIDES_EVENT))
}

function same(left: unknown, right: unknown) {
  return left === right || JSON.stringify(left) === JSON.stringify(right)
}

/** Records a settings write; call the returned functions when it settles. */
export function trackUserSettingsWrite(workspaceKey: string, patch: Partial<UserSettings>) {
  let fields = overrides.get(workspaceKey)
  if (!fields) overrides.set(workspaceKey, (fields = new Map()))
  const written = new Map<string, Override>()
  for (const [field, value] of Object.entries(patch)) {
    const entry: Override = { value }
    fields.set(field, entry)
    written.set(field, entry)
  }
  return {
    succeeded() {
      const now = Date.now()
      for (const entry of written.values()) entry.settledAt = now
    },
    failed() {
      // Only drop entries a later write hasn't replaced.
      for (const [field, entry] of written) if (fields.get(field) === entry) fields.delete(field)
      notify()
    },
  }
}

/** Server settings with any newer local writes laid over them. */
export function overlayUserSettings<T extends Partial<UserSettings>>(workspaceKey: string, settings: T, now = Date.now()): T {
  const fields = overrides.get(workspaceKey)
  if (!fields?.size) return settings
  let next: T | undefined
  for (const [field, entry] of fields) {
    const serverValue = (settings as Record<string, unknown>)[field]
    if (entry.settledAt !== undefined && (same(serverValue, entry.value) || now - entry.settledAt > SETTLED_GRACE_MS)) {
      fields.delete(field)
      continue
    }
    if (same(serverValue, entry.value)) continue
    next ??= { ...settings }
    ;(next as Record<string, unknown>)[field] = entry.value
  }
  return next ?? settings
}

/** Test helper. */
export function resetUserSettingsOverrides() {
  overrides.clear()
}
