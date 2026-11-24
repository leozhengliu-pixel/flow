import type { MouseEvent } from 'react'
import { useEffect, useRef } from 'react'
import { useOptionalTranslate } from '@/i18n/i18n'
import './skip-to-content.css'

/** The first Tab stop: jumps past the sidebar to the main content. Hidden until it takes focus. */
export function SkipToContent() {
  const t = useOptionalTranslate()
  const pending = useRef<MutationObserver | null>(null)
  const cancel = () => {
    pending.current?.disconnect()
    pending.current = null
  }
  useEffect(() => () => pending.current?.disconnect(), [])

  const skip = (event: MouseEvent<HTMLAnchorElement>) => {
    event.preventDefault()
    cancel()
    const focusContent = () => {
      for (const main of document.querySelectorAll<HTMLElement>('main:not([aria-busy="true"])')) {
        main.tabIndex = -1
        main.focus({ preventScroll: true })
        if (document.activeElement === main) {
          cancel()
          return true
        }
      }
      return false
    }
    if (!focusContent()) {
      // Lazy routes replace their loading landmark. Focus the loaded main,
      // provided the user has not already moved past the link.
      pending.current = new MutationObserver(focusContent)
      pending.current.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['aria-busy'] })
    }
  }
  return <a className="skip-to-content" href="#main" onClick={skip} onBlur={cancel}>{t('Skip to content')}</a>
}
