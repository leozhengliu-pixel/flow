import { startTransition, useCallback, useEffect, useRef } from 'react'
import { useLocation, useNavigate, type NavigateFunction, type NavigateOptions, type To } from 'react-router-dom'
import { nextNavigationState } from '@/lib/navigation-context'
import { clientNavigationTarget, preloadRoute } from '@/lib/route-preload'

export function useRouteNavigation() {
  const navigate = useNavigate()
  const location = useLocation()
  const current = useRef(location)
  current.current = location
  const navigateTo = useCallback((to: To | number, options?: NavigateOptions) => {
    if (typeof to === 'string') void preloadRoute(to).catch(() => undefined)
    startTransition(() => {
      if (typeof to === 'number') void navigate(to)
      else {
        const target = typeof to === 'string' ? to : `${to.pathname ?? current.current.pathname}${to.search ?? ''}${to.hash ?? ''}`
        const state = target.startsWith('/') && !target.startsWith('//') ? nextNavigationState(current.current, target, options?.state, options?.replace) : options?.state
        void navigate(to, { ...options, state })
      }
    })
  }, [navigate]) as NavigateFunction

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    let pendingAnchor: HTMLAnchorElement | undefined
    const clear = () => { clearTimeout(timer); pendingAnchor = undefined }
    const intent = (event: Event) => {
      const anchor = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null
      if (anchor === pendingAnchor) return
      clear()
      if (!anchor) return
      const path = clientNavigationTarget(anchor, new URL(window.location.href))
      if (!path) return
      pendingAnchor = anchor
      timer = setTimeout(() => { void preloadRoute(path).catch(() => undefined) }, event.type === 'focusin' ? 0 : 100)
    }
    const leave = (event: MouseEvent) => {
      if (pendingAnchor && (!(event.relatedTarget instanceof Node) || !pendingAnchor.contains(event.relatedTarget))) clear()
    }
    // React handlers and unsaved-editor guards run first. Only handle unclaimed links.
    const click = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
      const anchor = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null
      if (!anchor) return
      const path = clientNavigationTarget(anchor, new URL(window.location.href))
      if (!path) return
      event.preventDefault()
      void navigateTo(path)
    }
    document.addEventListener('pointerover', intent, { passive: true })
    document.addEventListener('focusin', intent)
    document.addEventListener('pointerout', leave)
    document.addEventListener('click', click)
    return () => {
      clear()
      document.removeEventListener('pointerover', intent)
      document.removeEventListener('focusin', intent)
      document.removeEventListener('pointerout', leave)
      document.removeEventListener('click', click)
    }
  }, [navigateTo])
  return navigateTo
}
