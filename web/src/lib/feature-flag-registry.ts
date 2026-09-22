/**
 * LS-0263 FeatureFlagDrawer support — local registry + ClientStorage overrides.
 * Dev-only; distinct from workspace settings boolean toggles.
 */
import { ClientStorage } from '@/lib/client-storage'

export const FEATURE_FLAG_OVERRIDES_KEY = 'FeatureFlags'

export type FeatureFlagDefinition = {
  key: string
  label: string
  description?: string
  /** Default from workspace / build when no override. */
  defaultEnabled: boolean
  maintainer?: string
  status?: 'development' | 'beta' | 'public' | 'internal'
}

export type FeatureFlagOverrideMap = Record<string, boolean>

/** Built-in registry — extend as product flags land. */
export const DEFAULT_FEATURE_FLAG_REGISTRY: FeatureFlagDefinition[] = [
  {
    key: 'loops',
    label: 'Loops',
    description: 'Agent automation loops surface',
    defaultEnabled: true,
    status: 'public',
  },
  {
    key: 'asks',
    label: 'Asks',
    description: 'Customer asks intake',
    defaultEnabled: true,
    status: 'public',
  },
  {
    key: 'triage-intelligence',
    label: 'Triage Intelligence',
    description: 'AI suggestions in triage accept',
    defaultEnabled: false,
    status: 'beta',
  },
  {
    key: 'dashboards',
    label: 'Dashboards',
    description: 'Workspace dashboards',
    defaultEnabled: true,
    status: 'public',
  },
  {
    key: 'releases',
    label: 'Releases',
    description: 'Release management',
    defaultEnabled: true,
    status: 'public',
  },
  {
    key: 'customer-requests',
    label: 'Customer requests',
    description: 'Issue customer needs',
    defaultEnabled: true,
    status: 'public',
  },
]

export function readFeatureFlagOverrides(): FeatureFlagOverrideMap {
  const raw = ClientStorage.get<FeatureFlagOverrideMap>(FEATURE_FLAG_OVERRIDES_KEY)
  if (!raw || typeof raw !== 'object') return {}
  const next: FeatureFlagOverrideMap = {}
  for (const [key, value] of Object.entries(raw)) {
    if (typeof value === 'boolean') next[key] = value
  }
  return next
}

export function writeFeatureFlagOverrides(map: FeatureFlagOverrideMap) {
  ClientStorage.set(FEATURE_FLAG_OVERRIDES_KEY, map)
}

export function clearFeatureFlagOverrides() {
  ClientStorage.remove(FEATURE_FLAG_OVERRIDES_KEY)
}

export function setFeatureFlagOverride(key: string, enabled: boolean | null) {
  const map = readFeatureFlagOverrides()
  if (enabled === null) delete map[key]
  else map[key] = enabled
  if (Object.keys(map).length === 0) clearFeatureFlagOverrides()
  else writeFeatureFlagOverrides(map)
}

export function resolveFeatureFlagEnabled(
  def: FeatureFlagDefinition,
  workspaceFlags?: Record<string, boolean>,
  overrides: FeatureFlagOverrideMap = readFeatureFlagOverrides(),
): { enabled: boolean; overridden: boolean } {
  if (Object.prototype.hasOwnProperty.call(overrides, def.key)) {
    return { enabled: overrides[def.key]!, overridden: true }
  }
  if (workspaceFlags && Object.prototype.hasOwnProperty.call(workspaceFlags, def.key)) {
    return { enabled: Boolean(workspaceFlags[def.key]), overridden: false }
  }
  return { enabled: def.defaultEnabled, overridden: false }
}

export function buildRegistryFromWorkspace(
  workspaceFlags: Record<string, boolean> | undefined,
  base: FeatureFlagDefinition[] = DEFAULT_FEATURE_FLAG_REGISTRY,
): FeatureFlagDefinition[] {
  const known = new Set(base.map(item => item.key))
  const extras: FeatureFlagDefinition[] = []
  for (const [key, value] of Object.entries(workspaceFlags ?? {})) {
    if (known.has(key)) continue
    extras.push({
      key,
      label: key,
      defaultEnabled: Boolean(value),
      status: 'internal',
    })
  }
  return [
    ...base.map(item => ({
      ...item,
      defaultEnabled: workspaceFlags?.[item.key] ?? item.defaultEnabled,
    })),
    ...extras.sort((a, b) => a.key.localeCompare(b.key)),
  ]
}
