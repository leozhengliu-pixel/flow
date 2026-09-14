import type { ReactNode } from 'react'
import { SidebarShell } from '@/components/layout/sidebar-shell'

/** App chrome used before workspace bootstrap settles. Always keeps a visible left nav. */
export function WorkspaceBootShell({
  children,
  sidebarLabel,
}: {
  children: ReactNode
  sidebarLabel?: string
}) {
  return (
    <div className="app loading-app">
      <SidebarShell label={sidebarLabel} />
      <main className="main-panel">{children}</main>
    </div>
  )
}
