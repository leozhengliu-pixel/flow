import { useEffect, useRef, useState } from 'react'

/** Property pickers the project page opens from the keyboard (Linear: P then S/P/A/M/L, Ctrl ⌥ S/D). */
export type ProjectPickerKind = 'status' | 'priority' | 'lead' | 'members' | 'labels' | 'startDate' | 'targetDate'
export type ProjectPickerRequest = { kind: ProjectPickerKind; id: number }

/** Second key of the "P then …" sequences. */
export const PROJECT_PICKER_SEQUENCES: Record<string, ProjectPickerKind> = {
  s: 'status',
  p: 'priority',
  a: 'lead',
  m: 'members',
  l: 'labels',
}

/** How long the first key of a sequence stays armed (matches the app-wide G/N sequences). */
export const PROJECT_SEQUENCE_TIMEOUT = 1100

export function isMacPlatform() {
  if (typeof navigator === 'undefined') return true
  const platform = (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ?? navigator.platform ?? ''
  return /mac|iphone|ipad|ipod/i.test(platform || navigator.userAgent)
}

/** Shortcut labels as Linear shows them in tooltips, per platform. */
export function projectShortcutLabels(mac = isMacPlatform()) {
  return {
    copyUrl: mac ? '⌘ ⇧ C' : 'Ctrl Shift C',
    toggleDetails: mac ? '⌘ I' : 'Ctrl I',
    startDate: mac ? 'Ctrl ⌥ S' : 'Ctrl Alt S',
    targetDate: mac ? 'Ctrl ⌥ D' : 'Ctrl Alt D',
    status: 'P then S',
    priority: 'P then P',
    lead: 'P then A',
    members: 'P then M',
    labels: 'P then L',
  }
}

const EDITABLE = 'input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"]'

export function isEditableShortcutTarget(target: EventTarget | null) {
  const element = target instanceof Element ? target : null
  return Boolean(element?.closest(EDITABLE) || (element instanceof HTMLElement && element.isContentEditable))
}

/** A dialog, menu or picker is open; tooltips (also popper-positioned) don't count. */
export function hasOpenProjectOverlay() {
  if (document.querySelector('[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"]')) return true
  return [...document.querySelectorAll('[data-radix-popper-content-wrapper]')].some(wrapper => !wrapper.querySelector('[role="tooltip"]'))
}

/** Which modifier-free key after "P" (or modifier chord) maps to a picker, or undefined. */
export function projectDateShortcut(event: Pick<KeyboardEvent, 'ctrlKey' | 'altKey' | 'metaKey' | 'shiftKey' | 'code' | 'key'>): ProjectPickerKind | undefined {
  if (!event.ctrlKey || !event.altKey || event.metaKey || event.shiftKey) return undefined
  const key = event.code === 'KeyS' || event.key.toLowerCase() === 's' ? 's' : event.code === 'KeyD' || event.key.toLowerCase() === 'd' ? 'd' : ''
  return key === 's' ? 'startDate' : key === 'd' ? 'targetDate' : undefined
}

export function isCopyProjectUrlShortcut(event: Pick<KeyboardEvent, 'ctrlKey' | 'altKey' | 'metaKey' | 'shiftKey' | 'code' | 'key'>) {
  return (event.metaKey || event.ctrlKey) && event.shiftKey && !event.altKey && (event.code === 'KeyC' || event.key.toLowerCase() === 'c')
}

/**
 * Controlled open state for one picker that also opens when the page requests it from the keyboard.
 * `onHandled` clears the request so a remount doesn't reopen the picker.
 */
export function useProjectPickerOpen(kind: ProjectPickerKind, request?: ProjectPickerRequest, onHandled?: () => void) {
  const [open, setOpen] = useState(false)
  const requestId = request?.kind === kind ? request.id : undefined
  const handled = useRef(onHandled)
  useEffect(() => { handled.current = onHandled })
  // Only a new request id reopens the picker.
  useEffect(() => {
    if (requestId === undefined) return
    setOpen(true)
    handled.current?.()
  }, [requestId])
  return [open, setOpen] as const
}
