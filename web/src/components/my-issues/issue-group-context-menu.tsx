import type { ReactElement } from 'react'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { useOptionalTranslate } from '@/i18n/i18n'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import styles from './issue-group-context-menu.module.css'

export interface IssueGroupMenuActions {
  onCollapseAll?: () => void
  onSelectAll?: () => void
  onHideGroup?: () => void
}

export function IssueGroupContextMenu({ children, collapsed, onCollapse, onCollapseAll, onSelectAll, onHideGroup }: IssueGroupMenuActions & { children: ReactElement; collapsed: boolean; onCollapse?: () => void }) {
  const t = useOptionalTranslate()
  const phone = usePhoneLayout()
  if (phone || !onCollapse) return children
  return <ContextMenu.Root>
    <ContextMenu.Trigger asChild onKeyDown={event => {
      if (event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')) return
      event.preventDefault()
      const rect = event.currentTarget.getBoundingClientRect()
      event.currentTarget.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: rect.left + 16, clientY: rect.bottom }))
    }}>{children}</ContextMenu.Trigger>
    <ContextMenu.Portal>
      <ContextMenu.Content data-flow-motion="floating" className={styles.menu} collisionPadding={8}>
        <ContextMenu.Item className={styles.item} onSelect={onCollapse}>{t(collapsed ? 'Expand' : 'Collapse')}</ContextMenu.Item>
        {onCollapseAll && <ContextMenu.Item className={styles.item} onSelect={onCollapseAll}>{t('Collapse all')}</ContextMenu.Item>}
        {onSelectAll && <ContextMenu.Item className={styles.item} onSelect={onSelectAll}>{t('Select all in group')}</ContextMenu.Item>}
        {onHideGroup && <ContextMenu.Item className={styles.item} onSelect={onHideGroup}>{t('Hide group')}</ContextMenu.Item>}
      </ContextMenu.Content>
    </ContextMenu.Portal>
  </ContextMenu.Root>
}
