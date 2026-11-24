import type { Page } from '@playwright/test'

export function observeIssueListPaint(profile: boolean) {
  const visibility = new IntersectionObserver(entries => {
    const visible = entries.find(entry => entry.isIntersecting && entry.intersectionRatio > 0)
    if (!visible) return
    // Use the rendering update's timestamp without forcing a layout.
    performance.mark('flow:test:issue-content', { startTime: visible.time })
    visibility.disconnect()
    rows.disconnect()
  })
  const rows = new MutationObserver(() => {
    const issues = document.querySelectorAll('[data-issue-row-id]')
    if (profile && issues.length && !performance.getEntriesByName('flow:test:issue-dom').length) {
      performance.mark('flow:test:issue-dom')
    }
    issues.forEach(row => visibility.observe(row))
  })
  rows.observe(document, { subtree: true, childList: true })
}

export function measureDetailOpen(page: Page, id: string, comment: string) {
  return page.evaluate(({ id, comment }) => {
    const onClick = (event: MouseEvent) => {
      const hit = (event.target as Element).closest('[data-issue-row-id]')
      if (hit?.getAttribute('data-issue-row-id') !== id) return
      document.removeEventListener('click', onClick, true)
      performance.mark('fixture:detail-click')
      const visible = new IntersectionObserver(entries => {
        const first = entries.find(entry => entry.isIntersecting && entry.intersectionRatio > 0)
        if (!first) return
        performance.measure('fixture:detail-open', { start: 'fixture:detail-click', end: first.time })
        visible.disconnect()
        clearTimeout(timeout)
      })
      const tree = new MutationObserver(() => {
        const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
        while (walker.nextNode()) {
          const node = walker.currentNode
          if (node.textContent === comment && node.parentElement) {
            visible.observe(node.parentElement)
            tree.disconnect()
            return
          }
        }
      })
      const timeout = setTimeout(() => { tree.disconnect(); visible.disconnect() }, 10_000)
      tree.observe(document.body, { subtree: true, childList: true, characterData: true })
    }
    document.addEventListener('click', onClick, true)
  }, { id, comment })
}
