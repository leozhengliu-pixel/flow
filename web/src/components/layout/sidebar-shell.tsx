import { useLayoutEffect, useState } from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import './sidebar.css'
import './sidebar-shell.css'

const widthKey = 'flow.sidebar.width'
const collapsedKey = 'flow.sidebar.collapsed'
const compactQuery = '(max-width: 1024px)'

function readShellWidth() {
  try {
    const stored = localStorage.getItem(widthKey)
    const width = stored === null ? 244 : Number(stored)
    if (Number.isFinite(width) && width > 64) return Math.max(220, Math.min(330, width))
  } catch { /* Preferences are optional. */ }
  return 244
}

function readCollapsed() {
  try { return localStorage.getItem(collapsedKey) === 'true' } catch { return false }
}

/** Visible left-nav placeholder while account/bootstrap are still resolving. */
export function SidebarShell({ label = 'Loading workspace navigation' }: { label?: string } = {}) {
  const [width] = useState(readShellWidth)
  const [collapsed] = useState(readCollapsed)
  const [compact] = useState(() => typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(compactQuery).matches)
  const rail = compact ? 0 : collapsed ? 8 : width

  useLayoutEffect(() => {
    const root = document.documentElement
    root.dataset.sidebarMode = compact ? 'compact' : collapsed ? 'collapsed' : 'expanded'
    root.style.setProperty('--sidebar', `${rail}px`)
    return () => {
      delete root.dataset.sidebarMode
      root.style.removeProperty('--sidebar')
    }
  }, [compact, collapsed, rail])

  return (
    <div className="sidebar-layout sidebar-shell-layout">
      <aside className="sidebar sidebar-shell" aria-busy="true" aria-label={label} role="navigation">
        <div className="workspace-row sidebar-shell-workspace">
          <Skeleton className="sidebar-shell-avatar" />
          <Skeleton className="sidebar-shell-workspace-name" />
          <Skeleton className="sidebar-shell-action" />
          <Skeleton className="sidebar-shell-action" />
        </div>
        <nav className="sidebar-nav sidebar-shell-nav" aria-hidden="true">
          {Array.from({ length: 6 }, (_, index) => (
            <div className="sidebar-shell-link" key={index}>
              <Skeleton className="sidebar-shell-link-icon" />
              <Skeleton className="sidebar-shell-link-label" />
            </div>
          ))}
          <div className="sidebar-shell-section">
            <Skeleton className="sidebar-shell-section-label" />
            {Array.from({ length: 4 }, (_, index) => (
              <div className="sidebar-shell-link" key={`section-${index}`}>
                <Skeleton className="sidebar-shell-link-icon" />
                <Skeleton className="sidebar-shell-link-label" />
              </div>
            ))}
          </div>
        </nav>
        <span className="sr-only">{label}</span>
      </aside>
    </div>
  )
}
