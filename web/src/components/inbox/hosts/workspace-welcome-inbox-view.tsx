import { useI18n } from '@/i18n/i18n'
import './workspace-welcome-inbox-view.css'

export function WorkspaceWelcomeInboxView({ title, message }: { title: string; message: string }) {
  const { t } = useI18n()
  return <article className="flow-inbox-welcome">
    <div className="flow-inbox-welcome__mark" aria-hidden="true">F</div>
    <h1>{title}</h1>
    <p>{message || t('Watch an introductory video and access a list of resources below.')}</p>
    <div className="flow-inbox-welcome__video" role="img" aria-label={t('Welcome video')}>
      <span>{t('Welcome video')}</span>
      <div className="flow-inbox-welcome__controls" aria-hidden="true"><b>▶</b><i/><small>0:00</small><em/></div>
    </div>
    <section className="flow-inbox-welcome__resources">
      <h2>{t('Resources')}</h2>
      <ul>
        <li><a href="https://flow.heliumlabz.com" target="_blank" rel="noreferrer">{t('Join a live onboarding session')}</a><span>{t('Learn the essentials and see demos of core workflows')}</span></li>
        <li><a href="https://flow.heliumlabz.com" target="_blank" rel="noreferrer">{t('Join our community')}</a></li>
      </ul>
    </section>
  </article>
}
