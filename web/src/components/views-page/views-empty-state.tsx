import type { ViewsResource } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import styles from './views-page.module.css'

type ViewsEmptyStateProps = {
  onCreate: () => void
  resource: ViewsResource
}

export function ViewsEmptyState({ onCreate, resource }: ViewsEmptyStateProps) {
  const { locale, t } = useI18n()
  const description = resource === 'issues'
    ? 'Create custom views using filters to show only the issues you want to see. You can save, share, and favorite these views for easy access and faster team collaboration.'
    : 'Create custom views using filters to show only the projects you want to see. You can save, share, and favorite these views for easy access and faster team collaboration.'

  return <div className={styles.emptyState}>
    <div className={styles.emptyIllustration}>
      <EmptyViewsIllustration />
    </div>
    <div className={styles.emptyBody}>
      <div className={styles.emptyCopy}>
        <span className={styles.emptyTitle}>Views</span>
        <div className={styles.emptyParagraphs}>
          <span data-i18n-ignore>{t(description)}</span>
          <span data-i18n-ignore>{locale === 'zh-CN' ? <>你也可以点击 <SaveViewIcon /> 图标，或按下 <ViewShortcut/> 保存任何现有视图。</> : <>You can also save any existing view by clicking the <SaveViewIcon /> icon or by pressing <ViewShortcut/>.</>}</span>
        </div>
      </div>
      <div className={styles.emptyActions}>
        <button className={styles.emptyPrimary} onClick={onCreate} type="button">{t('Create new view')}</button>
        <a className={styles.emptySecondary} href="https://flow.app/docs/custom-views" rel="noopener noreferrer" target="_blank">{t('Documentation')}</a>
      </div>
    </div>
  </div>
}

function ViewShortcut() { return <span className={styles.shortcut} aria-label="Option V"><span className={styles.shortcutKeys}><span className={styles.shortcutLabel}>Option V</span><kbd aria-hidden="true">⌥</kbd><kbd aria-hidden="true">V</kbd></span></span> }

function SaveViewIcon() {
  return <svg aria-hidden="true" className={styles.saveViewIcon} fill="currentColor" viewBox="0 0 16 16">
    <path clipRule="evenodd" d="M6.97358 1.34476C7.57022.885624 8.41055.885024 9.00788 1.3433L14.5499 5.59521C15.15 6.05565 15.15 6.94435 14.5499 7.40478L9.00788 11.6567C8.41055 12.115 7.57022 12.1144 6.97358 11.6552L1.44875 7.40374C.850417 6.94331.850415 6.05669 1.44875 5.59625L6.97358 1.34476ZM8 3.25C8.41421 3.25 8.75 3.58579 8.75 4V5.75H10.5C10.9142 5.75 11.25 6.08579 11.25 6.5C11.25 6.91421 10.9142 7.25 10.5 7.25H8.75V9C8.75 9.41421 8.41421 9.75 8 9.75C7.58579 9.75 7.25 9.41421 7.25 9V7.25H5.5C5.08579 7.25 4.75 6.91421 4.75 6.5C4.75 6.08579 5.08579 5.75 5.5 5.75H7.25V4C7.25 3.58579 7.58579 3.25 8 3.25Z" fillRule="evenodd" />
    <path d="M1.15024 9.79849c.24384-.33474.69848-.39736 1.01548-.13987l4.34409 3.33628c.78087.6343 1.8682.6343 2.64907 0l4.67552-3.33628c.3169-.25749.7716-.19487 1.0154.13987.2439.33471.1846.81481-.1324 1.07231l-4.6755 3.3363c-1.30145 1.0572-3.11366 1.0572-4.41512 0l-4.34409-3.3363c-.316992-.2575-.376293-.7376-.13245-1.07231Z" />
  </svg>
}

function EmptyViewsIllustration() {
  return <svg aria-label="Empty custom views list illustration" fill="none" viewBox="15 14 92 112">
    <path d="M20 110.4a2 2 0 0 1-1.26-1.85v-2.5a3 3 0 0 1 2.7-2.99L105 94.75v4.4a2 2 0 0 1-1 1.73l-41.78 24a6 6 0 0 1-5.22.37l-37-14.84Z" fill="var(--views-empty-fill)" stroke="var(--views-empty-stroke-1)" strokeWidth="1.5" />
    <path d="M19.88 106.41a2 2 0 0 1-.27-3.6L61.8 78.5a6 6 0 0 1 5.18-.4l37.13 14.5a2 2 0 0 1 .27 3.6L62.2 120.5a6 6 0 0 1-5.18.4l-37.13-14.5Z" fill="var(--views-empty-fill)" stroke="var(--views-empty-stroke-1)" strokeWidth="1.5" />
    <path d="M20 99.46a2 2 0 0 1-1.26-1.86v-2.5a3 3 0 0 1 2.7-2.99L105 83.8v4.4a2 2 0 0 1-1 1.73l-41.78 24a6 6 0 0 1-5.22.37L20 99.46Z" fill="var(--views-empty-fill)" stroke="var(--views-empty-stroke-2)" strokeWidth="1.5" />
    <path d="M19.88 95.46a2 2 0 0 1-.27-3.6l42.2-24.33a6 6 0 0 1 5.18-.39l37.13 14.5a2 2 0 0 1 .27 3.6l-42.2 24.32a6 6 0 0 1-5.18.4l-37.13-14.5Z" fill="var(--views-empty-fill)" stroke="var(--views-empty-stroke-2)" strokeWidth="1.5" />
    <path d="M20 88.5a2 2 0 0 1-1.26-1.85v-2.5a3 3 0 0 1 2.7-3l83.55-8.3v4.4a2 2 0 0 1-1 1.73l-41.78 24a6 6 0 0 1-5.22.36l-37-14.84Z" fill="var(--views-empty-fill)" stroke="var(--views-empty-stroke-3)" strokeWidth="1.5" />
    <path d="M19.88 84.5a2 2 0 0 1-.27-3.59l42.2-24.33A6 6 0 0 1 67 56.2l37.13 14.5a2 2 0 0 1 .27 3.59L62.2 98.6a6 6 0 0 1-5.2.4L19.88 84.5Z" fill="var(--views-empty-fill)" stroke="var(--views-empty-stroke-3)" strokeWidth="1.5" />
    <path d="M20.14 72.9a2 2 0 0 1-2.02-.99l-1.25-2.16a3 3 0 0 1 .85-3.94l68.2-48.97 2.2 3.8a2 2 0 0 1 0 2.01L63.94 64.32a6 6 0 0 1-4.34 2.93l-39.46 5.64Z" fill="var(--views-empty-fill)" stroke="var(--views-empty-stroke-4)" strokeWidth="1.5" />
    <path d="M18.04 69.49a2 2 0 0 1-2.03-2.98L40.4 24.34a6 6 0 0 1 4.29-2.93l39.4-6.01a2 2 0 0 1 2.03 2.98L61.73 60.55a6 6 0 0 1-4.29 2.93l-39.4 6.01Z" fill="var(--views-empty-fill)" stroke="var(--views-empty-stroke-4)" strokeWidth="1.5" />
  </svg>
}
