import { ContentViewHeader } from '@/components/content-view/content-view-header'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { useI18n } from '@/i18n/i18n'
import { NotFoundIllustration } from './not-found-illustration'
import './route-not-found.css'

/**
 * The page a route that matches nothing (or a record that is gone) shows. On a phone it keeps the page header
 * with the navigation drawer button, so the page is never a dead end.
 */
export function RouteNotFound({
  title = 'Not found',
  description = 'We could not find the page you were looking for',
  onOpenSidebar,
}: {
  title?: string
  description?: string
  onOpenSidebar?: () => void
}) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  return <main className="main-panel route-not-found">
    {phone && onOpenSidebar ? <ContentViewHeader onOpenSidebar={onOpenSidebar}>{null}</ContentViewHeader> : null}
    <div className="route-not-found__body">
      <NotFoundIllustration />
      <div className="route-not-found__text">
        <strong>{t(title)}</strong>
        <span>{t(description)}</span>
      </div>
    </div>
  </main>
}
