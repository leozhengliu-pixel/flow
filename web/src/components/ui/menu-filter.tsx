import { useLayoutEffect, useRef, useState } from 'react'
import { useOptionalTranslate } from '@/i18n/i18n'

export function MenuFilter() {
  const t = useOptionalTranslate()
  const ref = useRef<HTMLInputElement>(null)
  const emptyRef = useRef<HTMLDivElement>(null)
  const [query, setQuery] = useState('')
  useLayoutEffect(() => {
    const menu = ref.current?.closest('[role="menu"]')
    if (!menu) return
    const rows = [...menu.querySelectorAll<HTMLElement>('[role="menuitem"],[role="menuitemcheckbox"]')]
    const filter = query.trim().toLocaleLowerCase()
    for (const row of rows) row.toggleAttribute('data-menu-filter-hidden', !row.textContent?.toLocaleLowerCase().includes(filter))
    const structure = [...menu.querySelectorAll<HTMLElement>('[role="separator"],[data-menu-heading]')]
    for (const node of structure) node.toggleAttribute('data-menu-filter-hidden', Boolean(filter))
    if (filter) {
      let seenRow = false, separator: HTMLElement | undefined, heading: HTMLElement | undefined
      for (const node of menu.querySelectorAll<HTMLElement>('[role="menuitem"],[role="menuitemcheckbox"],[role="separator"],[data-menu-heading]')) {
        if (node.getAttribute('role') === 'separator') { separator = node; continue }
        if (node.hasAttribute('data-menu-heading')) { heading = node; continue }
        if (node.hasAttribute('data-menu-filter-hidden')) continue
        heading?.removeAttribute('data-menu-filter-hidden')
        heading = undefined
        if (seenRow) separator?.removeAttribute('data-menu-filter-hidden')
        separator = undefined
        seenRow = true
      }
    }
    if (emptyRef.current) emptyRef.current.hidden = !filter || rows.some(row => !row.hasAttribute('data-menu-filter-hidden'))
    return () => { for (const node of [...rows, ...structure]) node.removeAttribute('data-menu-filter-hidden') }
  }, [query])
  return <><div className="phone-menu-search"><input ref={ref} aria-label={t('Filter…')} placeholder={t('Filter…')} value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => {
    if (event.key === 'Escape' || event.key === 'Tab') return
    event.stopPropagation()
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const items = ref.current?.closest('[role="menu"]')?.querySelectorAll<HTMLElement>('[role^="menuitem"]:not([data-menu-filter-hidden]):not([data-disabled])')
      ;(event.key === 'ArrowUp' ? items?.[items.length - 1] : items?.[0])?.focus()
    }
  }}/></div><div ref={emptyRef} className="phone-menu-filter-empty" role="status" hidden>{t('No results')}</div></>
}
