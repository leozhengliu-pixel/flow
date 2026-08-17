import { useEffect, useRef, type RefObject } from 'react'

export type DismissReason = 'escape' | 'outside-pointer'

type Layer = {
  id: symbol
  refs: React.MutableRefObject<readonly RefObject<Element | null>[]>
  onDismiss: React.MutableRefObject<(reason: DismissReason, event: Event) => void>
  restoreFocusRef: React.MutableRefObject<RefObject<HTMLElement | null> | undefined>
  closeOnEscape: boolean
  closeOnPointerOutside: boolean
}

const layers: Layer[] = []
let listening = false

function isEventInside(layer: Layer, event: Event) {
  const path = event.composedPath()
  return layer.refs.current.some(ref => {
    const element = ref.current
    if (!element) return false
    return path.includes(element) || path.some(target => target instanceof Node && element.contains(target))
  })
}

function handlePointerDown(event: PointerEvent) {
  const snapshot = [...layers]
  for (let index = snapshot.length - 1; index >= 0; index -= 1) {
    const layer = snapshot[index]
    if (isEventInside(layer, event)) break
    if (layer.closeOnPointerOutside) layer.onDismiss.current('outside-pointer', event)
  }
}

function handleKeyDown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.defaultPrevented) return
  const layer = [...layers].reverse().find(candidate => candidate.closeOnEscape)
  if (!layer) return
  event.preventDefault()
  event.stopPropagation()
  layer.onDismiss.current('escape', event)
  const restoreFocusRef = layer.restoreFocusRef.current
  if (restoreFocusRef) requestAnimationFrame(() => restoreFocusRef.current?.focus())
}

function startListening() {
  if (listening) return
  document.addEventListener('pointerdown', handlePointerDown, true)
  document.addEventListener('keydown', handleKeyDown, true)
  listening = true
}

function stopListening() {
  if (!listening || layers.length) return
  document.removeEventListener('pointerdown', handlePointerDown, true)
  document.removeEventListener('keydown', handleKeyDown, true)
  listening = false
}

export function useDismissibleLayer({
  open,
  refs,
  onDismiss,
  restoreFocusRef,
  closeOnEscape = true,
  closeOnPointerOutside = true,
}: {
  open: boolean
  refs: readonly RefObject<Element | null>[]
  onDismiss: (reason: DismissReason, event: Event) => void
  restoreFocusRef?: RefObject<HTMLElement | null>
  closeOnEscape?: boolean
  closeOnPointerOutside?: boolean
}) {
  const id = useRef(Symbol('dismissible-layer'))
  const refsRef = useRef(refs)
  const onDismissRef = useRef(onDismiss)
  const restoreFocusRefRef = useRef(restoreFocusRef)
  refsRef.current = refs
  onDismissRef.current = onDismiss
  restoreFocusRefRef.current = restoreFocusRef

  useEffect(() => {
    if (!open) return
    const layer: Layer = {
      id: id.current,
      refs: refsRef,
      onDismiss: onDismissRef,
      restoreFocusRef: restoreFocusRefRef,
      closeOnEscape,
      closeOnPointerOutside,
    }
    layers.push(layer)
    startListening()
    return () => {
      const index = layers.findIndex(candidate => candidate.id === layer.id)
      if (index >= 0) layers.splice(index, 1)
      stopListening()
    }
  }, [closeOnEscape, closeOnPointerOutside, open])
}
