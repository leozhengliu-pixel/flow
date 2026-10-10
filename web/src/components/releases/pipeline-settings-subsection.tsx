import type { ReactNode } from 'react'

/** A titled block inside a section (Linear's settings subsection): title, description, then its own control. */
export function Subsection({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return <div className="settings-group pipeline-settings-subsection" role="group" aria-label={title}>
    <header><strong>{title}</strong><p>{description}</p></header>
    {children}
  </div>
}
