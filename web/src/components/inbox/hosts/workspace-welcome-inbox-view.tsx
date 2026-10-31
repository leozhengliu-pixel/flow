import { useI18n } from '@/i18n/i18n'
import { FlowLogo } from '@/components/ui/flow-logo'
import './workspace-welcome-inbox-view.css'

export function WorkspaceWelcomeInboxView({ title, message }: { title: string; message: string }) {
  const { t } = useI18n()
  return <article className="flow-inbox-welcome">
    <div className="flow-inbox-welcome__mark" aria-hidden="true"><span>F</span><FlowLogo size={44}/></div>
    <h1>{title}</h1>
    <p>{message || t('Watch an introductory video and access a list of resources below.')}</p>
    <div className="flow-inbox-welcome__video" role="img" aria-label={t('Welcome video')}>
      <span className="flow-inbox-welcome__video-label">{t('Welcome video')}</span>
      <span className="flow-inbox-welcome__video-placeholder">{t('Introductory video unavailable')}</span>
      <div className="flow-inbox-welcome__controls" aria-hidden="true"><b>▶</b><i/><small>0:00</small><em/></div>
    </div>
    <section className="flow-inbox-welcome__resources">
      <h2>{t('Resources')}</h2>
      <ul>
        <li><a href="https://github.com/leozhengliu-pixel/flow/tree/main/docs" target="_blank" rel="noreferrer">{t('Flow documentation')}</a><span>{t('Learn the essentials and explore core workflows')}</span></li>
        <li><a href="https://github.com/leozhengliu-pixel/flow/issues" target="_blank" rel="noreferrer">{t('Community and support')}</a></li>
      </ul>
    </section>
  </article>
}
