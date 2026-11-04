import type { MouseEvent } from 'react'
import { useOptionalTranslate } from '@/i18n/i18n'
import './skip-to-content.css'

/** The first Tab stop: jumps past the sidebar to the main content. Hidden until it takes focus. */
export function SkipToContent() {
  const t = useOptionalTranslate()
  const skip = (event: MouseEvent<HTMLAnchorElement>) => {
    const main = document.querySelector<HTMLElement>('main')
    if (!main) return
    event.preventDefault()
    main.tabIndex = -1
    main.focus({ preventScroll: true })
  }
  return <a className="skip-to-content" href="#main" onClick={skip}>{t('Skip to content')}</a>
}
