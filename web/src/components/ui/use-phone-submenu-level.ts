import { useEffect } from 'react'
import { usePhoneLayout } from '@/hooks/use-phone-layout'

export function usePhoneSubmenuLevel(open: boolean, trigger: HTMLElement | null, content: HTMLElement | null) {
  const phone = usePhoneLayout()
  useEffect(() => {
    if (!phone || !open || !content) return
    const parent = trigger?.closest<HTMLElement>('[role="menu"], [data-phone-submenu], .phone-menu-surface')
    if (!parent) return
    // Move focus before hiding the trigger so its blur does not dismiss the submenu.
    if (parent.contains(document.activeElement)) (content.querySelector<HTMLElement>('input') ?? content).focus({ preventScroll: true })
    parent.setAttribute('data-phone-submenu-open', '')
    return () => {
      parent.removeAttribute('data-phone-submenu-open')
      const active = document.activeElement
      if (parent.isConnected && parent.getAttribute('data-state') !== 'closed' && (active === document.body || content.contains(active))) {
        const target = parent.querySelector<HTMLElement>('input:not(:disabled)') ?? (trigger?.isConnected ? trigger : parent)
        target.focus({ preventScroll: true })
      }
    }
  }, [content, open, phone, trigger])
  return phone
}
