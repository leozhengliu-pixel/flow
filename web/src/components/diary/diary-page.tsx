/**
 * LS-0199 DiaryPage — thin route wrapper → DiaryView.
 */
import type { BootstrapData } from '@/types/flow'
import { DiaryView } from './diary-view'

export function DiaryPage({
  data,
  onReload,
}: {
  data: BootstrapData
  onReload?: () => Promise<void>
}) {
  return <DiaryView data={data} onReload={onReload} />
}

DiaryPage.displayName = 'DiaryPage'

export default DiaryPage
