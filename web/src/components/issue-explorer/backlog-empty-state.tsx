import { useI18n } from '@/i18n/i18n'
import styles from './backlog-empty-state.module.css'

export function BacklogEmptyState({ onCreateIssue }: { onCreateIssue?: () => void }) {
  const { t } = useI18n()
  return <div className={styles.empty} role="status"><div className={styles.body} data-backlog-introduction>
    <svg aria-hidden="true" className={styles.art} viewBox="0 0 86 80" fill="none">
      {[['20', '19'], ['68', '19'], ['20', '61'], ['68', '61']].map(([x, y], index) => <g key={index} transform={`translate(${x} ${y})`} className={index === 1 ? styles.highlight : undefined}>
        <path d="M-18 0v6c0 9 36 9 36 0V0"/>
        <ellipse rx="18" ry="14"/>
        <ellipse rx="13" ry="10" strokeDasharray="1.5 3"/>
      </g>)}
    </svg>
    <strong>{t('Backlog issues')}</strong>
    <p>{t('The backlog is a place for new issues and ideas that haven’t been prioritized yet.')}</p>
    <p>{t('When your team is ready to work on these issues, you can move them out of the backlog by updating their status or adding them to a Cycle.')}</p>
    <div className={styles.actions} data-backlog-actions>
      <button disabled={!onCreateIssue} onClick={onCreateIssue} type="button">{t('Create new issue')}<kbd aria-hidden="true">C</kbd></button>
      <a href="https://github.com/leozhengliu-pixel/flow/blob/main/docs/backlog.md" rel="noreferrer" target="_blank">{t('Documentation')}</a>
    </div>
  </div></div>
}
