import { useEffect, useLayoutEffect, useRef } from 'react'
import { useNavigationType } from 'react-router-dom'
import { useLatestRef } from '@/hooks/use-latest-ref'

export interface SplitViewListActivationOptions {
  isSplitView: boolean
  currentItemKey: string | null | undefined
  /** Return false to block activation for a key. */
  canActivate?: (itemKey: string) => boolean
  onActivate?: (itemKey: string) => void
  onDeactivate?: () => void
  /** When true, empty selection will not auto-fire onDeactivate. */
  isActiveStateFrozen?: boolean
}

/**
 * LS-0769 — activate/deactivate the split list selection from history transitions.
 * PUSH/REPLACE with a key activates; clearing the key on PUSH/REPLACE deactivates.
 */
export function useSplitViewListActivation({
  isSplitView,
  currentItemKey,
  canActivate,
  onActivate,
  onDeactivate,
  isActiveStateFrozen = false,
}: SplitViewListActivationOptions) {
  const navigationType = useNavigationType()
  const isHistoryWrite = navigationType === 'PUSH' || navigationType === 'REPLACE'
  const canActivateRef = useLatestRef(canActivate)
  const onActivateRef = useLatestRef(onActivate)
  const onDeactivateRef = useLatestRef(onDeactivate)
  const activeKeyRef = useRef<string | null>(null)
  const frozenRef = useLatestRef(isActiveStateFrozen)

  useLayoutEffect(() => {
    if (!isSplitView) {
      if (activeKeyRef.current != null) {
        activeKeyRef.current = null
        onDeactivateRef.current?.()
      }
      return
    }
    if (currentItemKey) {
      if (currentItemKey !== activeKeyRef.current) {
        if (canActivateRef.current?.(currentItemKey) ?? true) {
          activeKeyRef.current = currentItemKey
          onActivateRef.current?.(currentItemKey)
        }
      }
      return
    }
    if (isHistoryWrite && activeKeyRef.current != null) {
      activeKeyRef.current = null
      onDeactivateRef.current?.()
    }
  }, [currentItemKey, isHistoryWrite, isSplitView, canActivateRef, onActivateRef, onDeactivateRef])

  useEffect(() => {
    if (!isSplitView) return
    if (activeKeyRef.current == null && !isActiveStateFrozen && !frozenRef.current) {
      onDeactivateRef.current?.()
    }
  }, [isActiveStateFrozen, isSplitView, frozenRef, onDeactivateRef])

  return {
    /** The key the list selection is currently activated for; read it from handlers or effects, not while rendering. */
    getActiveItemKey: () => activeKeyRef.current,
    isHistoryWrite,
  }
}
