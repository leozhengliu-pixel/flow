import type { ReactNode } from 'react'
import { ChevronRight, RotateCcw } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import styles from './agent-chat-panel.module.css'

/** Linear's "Created draft" card: "<context> › <title>", a two-line preview, and Outdated/Restore once the host text moved on. */
export function AgentDraftCard({ context, icon, title, draft, outdated = false, onRestore }: { context: string; icon?: ReactNode; title: string; draft: string; outdated?: boolean; onRestore?: () => void }) {
  const { t } = useI18n()
  return (
    <div className={styles.draftUpdate}>
      <span className={styles.draftUpdateLabel}>{t('Created draft')}</span>
      <div className={styles.draftCard}>
        <div className={styles.draftCardTitle}>
          {icon}
          <span data-i18n-ignore>{context}</span>
          <ChevronRight aria-hidden="true" size={12} />
          <b>{t(title)}</b>
        </div>
        <p data-i18n-ignore>{draft}</p>
        {outdated && onRestore && (
          <div className={styles.draftCardFooter}>
            <span>{t('Outdated')}</span>
            <button onClick={onRestore} type="button"><RotateCcw aria-hidden="true" size={12} />{t('Restore')}</button>
          </div>
        )}
      </div>
    </div>
  )
}
