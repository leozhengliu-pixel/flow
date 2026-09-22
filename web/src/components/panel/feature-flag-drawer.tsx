/**
 * LS-0263 FeatureFlagDrawer — dev-only local override drawer (Features tab).
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { X } from 'lucide-react'
import { ElevatedPanel } from '@/components/panel/elevated-panel'
import {
  buildRegistryFromWorkspace,
  clearFeatureFlagOverrides,
  readFeatureFlagOverrides,
  resolveFeatureFlagEnabled,
  setFeatureFlagOverride,
  type FeatureFlagDefinition,
  type FeatureFlagOverrideMap,
} from '@/lib/feature-flag-registry'
import './feature-flag-drawer.css'

export type FeatureFlagDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceFlags?: Record<string, boolean>
  /** Force-enable outside import.meta.env.DEV (tests). */
  forceEnable?: boolean
}

export function FeatureFlagDrawer({
  open,
  onOpenChange,
  workspaceFlags,
  forceEnable = false,
}: FeatureFlagDrawerProps) {
  const enabled = forceEnable || import.meta.env.DEV
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState<'all' | 'enabled' | 'disabled' | 'overridden'>('all')
  const [overrides, setOverrides] = useState<FeatureFlagOverrideMap>(() => readFeatureFlagOverrides())

  const refresh = useCallback(() => {
    setOverrides(readFeatureFlagOverrides())
  }, [])

  useEffect(() => {
    if (open) refresh()
  }, [open, refresh])

  const registry = useMemo(
    () => buildRegistryFromWorkspace(workspaceFlags),
    [workspaceFlags],
  )

  const rows = useMemo(() => {
    const q = search.trim().toLowerCase()
    return registry.filter(def => {
      const { enabled: on, overridden } = resolveFeatureFlagEnabled(def, workspaceFlags, overrides)
      if (filter === 'enabled' && !on) return false
      if (filter === 'disabled' && on) return false
      if (filter === 'overridden' && !overridden) return false
      if (!q) return true
      return (
        def.key.toLowerCase().includes(q) ||
        def.label.toLowerCase().includes(q) ||
        (def.description ?? '').toLowerCase().includes(q)
      )
    })
  }, [filter, overrides, registry, search, workspaceFlags])

  if (!enabled || !open) return null

  const toggle = (def: FeatureFlagDefinition) => {
    const { enabled: on, overridden } = resolveFeatureFlagEnabled(def, workspaceFlags, overrides)
    if (overridden) setFeatureFlagOverride(def.key, null)
    else setFeatureFlagOverride(def.key, !on)
    refresh()
  }

  return (
    <div className="flow-feature-flag-drawer" role="dialog" aria-label="Feature flags">
      <button
        type="button"
        className="flow-feature-flag-drawer__backdrop"
        aria-label="Close feature flags"
        onClick={() => onOpenChange(false)}
      />
      <ElevatedPanel elevation={3} className="flow-feature-flag-drawer__panel" border="strong">
        <header className="flow-feature-flag-drawer__header">
          <div>
            <h2>Features</h2>
            <p>Local overrides — active until you clear storage.</p>
          </div>
          <button
            type="button"
            className="flow-feature-flag-drawer__icon"
            aria-label="Close"
            onClick={() => onOpenChange(false)}
          >
            <X size={16} />
          </button>
        </header>
        <div className="flow-feature-flag-drawer__toolbar">
          <input
            value={search}
            onChange={event => setSearch(event.target.value)}
            placeholder="Search feature flags…"
            aria-label="Search feature flags"
          />
          <div className="flow-feature-flag-drawer__filters" role="tablist" aria-label="Flag filters">
            {(
              [
                ['all', 'All'],
                ['enabled', 'Enabled'],
                ['disabled', 'Disabled'],
                ['overridden', 'Overridden'],
              ] as const
            ).map(([id, label]) => (
              <button
                key={id}
                type="button"
                role="tab"
                aria-selected={filter === id}
                className={filter === id ? 'is-active' : undefined}
                onClick={() => setFilter(id)}
              >
                {label}
              </button>
            ))}
          </div>
        </div>
        <ul className="flow-feature-flag-drawer__list">
          {rows.map(def => {
            const { enabled: on, overridden } = resolveFeatureFlagEnabled(
              def,
              workspaceFlags,
              overrides,
            )
            return (
              <li key={def.key}>
                <div>
                  <strong data-i18n-ignore>{def.label}</strong>
                  <small data-i18n-ignore>
                    {def.key}
                    {overridden ? ' (overridden)' : ''}
                    {def.status ? ` · ${def.status}` : ''}
                  </small>
                </div>
                <button type="button" onClick={() => toggle(def)}>
                  {overridden ? 'Clear override' : on ? 'Override off' : 'Override on'}
                </button>
              </li>
            )
          })}
          {!rows.length && <li className="flow-feature-flag-drawer__empty">No flags match.</li>}
        </ul>
        <footer className="flow-feature-flag-drawer__footer">
          <button
            type="button"
            onClick={() => {
              clearFeatureFlagOverrides()
              refresh()
            }}
          >
            Clear all local overrides
          </button>
        </footer>
      </ElevatedPanel>
    </div>
  )
}

/** Host: Ctrl/Cmd+Shift+F opens drawer when DEV. */
export function FeatureFlagDrawerHost({
  workspaceFlags,
}: {
  workspaceFlags?: Record<string, boolean>
}) {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    if (!import.meta.env.DEV) return
    const onKey = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || !event.shiftKey) return
      if (event.key.toLowerCase() !== 'f') return
      const target = event.target as HTMLElement | null
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable))
        return
      event.preventDefault()
      setOpen(value => !value)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  if (!import.meta.env.DEV) return null
  return (
    <FeatureFlagDrawer open={open} onOpenChange={setOpen} workspaceFlags={workspaceFlags} />
  )
}

export default FeatureFlagDrawer
