import { useState, type ReactNode } from 'react'

import { useI18n } from '@/i18n/i18n'

/** A collapsible section of the cycle details card: a title that toggles its body. */
export function CyclePanelSection({ title, collapseLabel, expandLabel, className, children }: { title: string; collapseLabel: string; expandLabel: string; className: string; children: ReactNode }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(true)
  return <section className={`cycle-panel-section ${className}`}>
    <button type="button" className="cycle-panel-section__title" aria-expanded={open} aria-label={t(open ? collapseLabel : expandLabel)} onClick={() => setOpen(value => !value)}>
      <span>{title}</span>
      <svg aria-hidden="true" viewBox="0 0 16 16"><path d="M5.7 6h4.6c.45 0 .7.5.45.85L8.5 10.4a.6.6 0 0 1-1 0L5.25 6.85C5 6.5 5.25 6 5.7 6Z"/></svg>
    </button>
    {open && <div className="cycle-panel-section__body">{children}</div>}
  </section>
}
