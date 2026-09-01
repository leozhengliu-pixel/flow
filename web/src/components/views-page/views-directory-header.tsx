import type { ReactNode } from 'react'
import { useI18n } from '@/i18n/i18n'

import styles from './views-page.module.css'

export type ViewsDirectoryResource = 'issues' | 'projects' | 'dashboards'

type ViewsDirectoryTab = {
  href: string
  label: string
  resource: ViewsDirectoryResource
  onSelect?: () => void
}

export function ViewsDirectoryHeader({
  activeResource,
  actionLabel,
  afterTitle,
  onAction,
  onOpenSidebar,
  tabs,
  title,
  toolbarEnd,
}: {
  activeResource: ViewsDirectoryResource
  actionLabel: string
  afterTitle?: ReactNode
  onAction: () => void
  onOpenSidebar?: () => void
  tabs: ViewsDirectoryTab[]
  title: string
  toolbarEnd?: ReactNode
}) {
  const { t } = useI18n()
  const localizedAction = t(actionLabel)
  return <>
    <header className={styles.header}>
      <button className={styles.mobileMenu} aria-label={t('Open workspace sidebar')} onClick={onOpenSidebar} type="button">{t('Menu')}</button>
      <h2>{t(title)}</h2>
      {afterTitle}
      <button aria-label={t(`Create ${actionLabel.toLowerCase()}`)} className={styles.createHeader} onClick={onAction} type="button">
        <FlowPlusIcon/>
        <span>{localizedAction}</span>
      </button>
    </header>
    <div className={styles.toolbar}>
      <nav aria-label={t('View resources')} className={styles.tabs}>
        {tabs.map(tab => <a
          aria-current={activeResource === tab.resource ? 'page' : undefined}
          className="ui-pill"
          href={tab.href}
          key={tab.resource}
          onClick={event => {
            if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
            event.preventDefault()
            if (activeResource !== tab.resource) tab.onSelect?.()
          }}
        >{t(tab.label)}</a>)}
      </nav>
      {toolbarEnd && <div className={styles.toolbarEnd}>{toolbarEnd}</div>}
    </div>
  </>
}

export function FlowPlusIcon() {
  return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M8.75 4C8.75 3.58579 8.41421 3.25 8 3.25C7.58579 3.25 7.25 3.58579 7.25 4V7.25H4C3.58579 7.25 3.25 7.58579 3.25 8C3.25 8.41421 3.58579 8.75 4 8.75H7.25V12C7.25 12.4142 7.58579 12.75 8 12.75C8.41421 12.75 8.75 12.4142 8.75 12V8.75H12C12.4142 8.75 12.75 8.41421 12.75 8C12.75 7.58579 12.4142 7.25 12 7.25H8.75V4Z"/></svg>
}
