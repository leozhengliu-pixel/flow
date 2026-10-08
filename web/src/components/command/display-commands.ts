/**
 * ⌘K "Display options › …" commands. The issue list registers what its display menu offers;
 * the command menu lists Linear's entries (Display options › Grouping › Customer,
 * Display options › View ordering › By customer count…).
 */
import { useEffect, useRef, useSyncExternalStore } from 'react'
import './display-commands.css'

export type DisplayCommandKind = 'grouping' | 'subGrouping' | 'ordering'

export interface DisplayCommand {
  id: string
  kind: DisplayCommandKind
  /** Breadcrumb after "Display options", e.g. ['Grouping', 'Customer'] or ['View ordering', 'By customer count']. */
  path: [string, string]
  /** Value key, used for the icon. */
  value: string
  current: boolean
  run: () => void
}

type Source = () => DisplayCommand[]

const sources = new Map<string, Source>()
const listeners = new Set<() => void>()
let version = 0
let sequence = 0

function emit() { version += 1; for (const listener of listeners) listener() }

export function getDisplayCommands(): DisplayCommand[] {
  const latest = [...sources.values()].at(-1)
  return latest ? latest() : []
}

export function registerDisplayCommands(source: Source) {
  const key = `display-${sequence += 1}`
  sources.set(key, source)
  emit()
  return () => { sources.delete(key); emit() }
}

/** Registers the surface's display commands while mounted; `source` is read lazily when ⌘K opens. */
export function useRegisterDisplayCommands(source: Source | undefined) {
  const ref = useRef(source)
  ref.current = source
  const enabled = Boolean(source)
  useEffect(() => enabled ? registerDisplayCommands(() => ref.current?.() ?? []) : undefined, [enabled])
}

export function useDisplayCommands(open: boolean) {
  useSyncExternalStore(listener => { listeners.add(listener); return () => listeners.delete(listener) }, () => version, () => version)
  return open ? getDisplayCommands() : []
}

/** Linear's command label for an ordering: "By customer count". */
export function orderingCommandLabel(label: string) {
  return `By ${label.charAt(0).toLowerCase()}${label.slice(1)}`
}
