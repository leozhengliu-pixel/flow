import { BookOpen, GitPullRequest, Inbox, RefreshCw, Sparkles } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import './reviews-onboarding.css'

export function ReviewsOnboarding({ onConnect }: { onConnect: (provider: 'github' | 'gitlab') => void }) {
  const { t } = useI18n()
  const features = [
    { title: 'Guided reviews', description: 'Review large PRs one meaningful section at a time. Guides show you what a PR changes and why.', Icon: BookOpen },
    { title: 'Review inbox', description: "See a streamlined list of PRs you've authored and PRs that need your review.", Icon: Inbox },
    { title: 'GitHub sync', description: 'Every PR comment, approval, and merge in Flow syncs back to GitHub.', Icon: RefreshCw },
    { title: 'Iterate', description: 'Ask Flow Agent to address comment feedback, and the diff updates in real time.', Icon: Sparkles },
  ]
  return <section className="reviews-onboarding" aria-label={t('Get started with reviews')}>
    <div className="reviews-onboarding-hero">
      <div className="reviews-onboarding-copy">
        <h1>{t('Your team is reviewing diffs in Flow')}</h1>
        <p>{t('Flow Diffs brings code review into the product development workflow. See pull requests alongside the issues behind them, review and discuss changes, and merge without switching between Flow and GitHub.')}</p>
        <div className="reviews-onboarding-actions">
          <button className="review-pill-button is-primary" aria-label={t('Connect personal GitHub account')} type="button" onClick={() => onConnect('github')}><GitPullRequest/>{t('Connect GitHub')}</button>
          <a className="review-pill-button" href="https://github.com/leozhengliu-pixel/flow/blob/main/docs/code-scanning-review.md" target="_blank" rel="noreferrer">{t('Learn more')}</a>
          <button className="review-pill-button" type="button" onClick={() => onConnect('gitlab')}>{t('Connect GitLab')}</button>
        </div>
      </div>
      <div className="reviews-onboarding-preview" role="img" aria-label={t('Review preview')}>
        <header><GitPullRequest size={18}/><strong>{t('Refactor notification model')}</strong></header>
        <span className="reviews-onboarding-preview-branch">main &larr; notification-model</span>
        <div className="reviews-onboarding-preview-content">
          <div><small>01 / 03</small><h2>{t('Shared notification model')}</h2><p>{t('Separate notification categories from delivery so each change is easier to review.')}</p><span>NotificationCategory.ts</span><span>Notification.ts</span></div>
          <pre><code>{'type Notification = {\n  id: string\n'}<del>{'  category: string\n'}</del><ins>{'  category: NotificationCategory\n'}</ins>{'  title: string\n  read: boolean\n}'}</code></pre>
        </div>
      </div>
    </div>
    <div className="reviews-onboarding-features">{features.map(({ title, description, Icon }) => <section key={title}><Icon size={24}/><h2>{t(title)}</h2><p>{t(description)}</p></section>)}</div>
  </section>
}
