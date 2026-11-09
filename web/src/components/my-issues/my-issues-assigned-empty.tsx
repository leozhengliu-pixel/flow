import { useI18n } from '@/i18n/i18n'
import styles from './my-issues-assigned-empty.module.css'

/** The empty Assigned tab: an original three-card stack with a tick, "No issues assigned to you" and a primary "Create new issue". */
export function MyIssuesAssignedEmpty({ onCreateIssue }: { onCreateIssue?: () => void }) {
  const { t } = useI18n()
  return <div className={styles.empty} role="status">
    <svg className={styles.art} viewBox="20 23 125 93" fill="none" aria-hidden="true">
      <rect className={styles.reflection} x="48" y="30" width="68" height="40" rx="6"/>
      <rect className={styles.face} x="40" y="44" width="84" height="46" rx="7"/>
      <rect className={styles.ringOuter} x="32" y="58" width="100" height="46" rx="8"/>
      <path className={styles.ringInner} d="M46 74h38M46 86h24" strokeLinecap="round"/>
      <circle className={styles.frame} cx="116" cy="82" r="7"/>
      <path className={styles.ringDashed} d="M36 112h92"/>
    </svg>
    <strong>{t('No issues assigned to you')}</strong>
    {onCreateIssue && <button type="button" onClick={onCreateIssue}>{t('Create new issue')}</button>}
  </div>
}
