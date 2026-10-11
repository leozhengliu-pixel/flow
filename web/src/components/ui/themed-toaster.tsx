import { useEffect, useState, type CSSProperties } from 'react'
import { useInRouterContext, useNavigate } from 'react-router-dom'
import { Toaster } from 'sonner'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import { chordMatches, parseShortcut } from '@/components/ui/menu-shortcuts'
import './toast.css'
import { TOAST_DURATION } from '@/lib/toast'
import { openLatestToastLink, OPEN_TOAST_LINK_SHORTCUT, setToastNavigator } from '@/lib/toast-registry'
import {
  getResolvedTheme,
  THEME_CHANGE_EVENT,
  type ResolvedTheme,
} from '@/lib/theme'

/** Bottom-right stack: 24px from the right edge, 52px above the bottom (44px / 16px on small screens), 8px apart. */
const OFFSET = { top: 24, right: 24, bottom: 52, left: 24 }
const MOBILE_OFFSET = { top: 16, right: 16, bottom: 44, left: 16 }
const TOASTER_STYLE = { '--width': '384px' } as CSSProperties
const OPEN_LINK_CHORD = parseShortcut(OPEN_TOAST_LINK_SHORTCUT)[0]

function RouterBridge() {
  const navigate = useNavigate()
  useEffect(() => {
    setToastNavigator(to => navigate(to))
    return () => setToastNavigator(null)
  }, [navigate])
  return null
}

/** ⌘ ⌥ O opens the newest toast's link ("View release", "Undo", or its entity chip). */
function useOpenToastLinkShortcut() {
  useEffect(() => {
    const mac = isMacPlatform()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !chordMatches(event, OPEN_LINK_CHORD, mac)) return
      if (openLatestToastLink()) event.preventDefault()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
}

export function ThemedToaster() {
  const [theme, setTheme] = useState<ResolvedTheme>(getResolvedTheme);
  const routed = useInRouterContext()
  useOpenToastLinkShortcut()
  useEffect(() => {
    const update = (event: Event) => setTheme((event as CustomEvent<ResolvedTheme>).detail);
    window.addEventListener(THEME_CHANGE_EVENT, update);
    return () => window.removeEventListener(THEME_CHANGE_EVENT, update);
  }, []);
  return <>
    {routed ? <RouterBridge/> : null}
    <Toaster
      className="flow-toaster"
      containerAriaLabel="Notifications"
      duration={TOAST_DURATION}
      expand
      gap={8}
      mobileOffset={MOBILE_OFFSET}
      offset={OFFSET}
      position="bottom-right"
      style={TOASTER_STYLE}
      theme={theme}
      visibleToasts={4}
    />
  </>
}
