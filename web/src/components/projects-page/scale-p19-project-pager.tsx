import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import './scale-p19-project-window.css'

export function ScaleP19ProjectPager({ page, loading, onPage, label = 'Projects' }: { page?: { index: number; offset?: number; items: readonly unknown[]; total: number; hasMore: boolean }; loading: boolean; onPage: (index: number) => void; label?: string }) {
  const { t } = useI18n()
  const offset = page?.offset ?? (page?.index ?? 0) * 100
  return <nav className="scale-p19-project-pager" aria-label={t(label)}>
    <span aria-live="polite">{page ? `${offset + (page.items.length ? 1 : 0)}–${offset + page.items.length} / ${page.total}` : t('Loading…')}</span>
    <button type="button" aria-label={t('Previous page')} title={t('Previous page')} disabled={loading || !page?.index} onClick={() => onPage(page!.index - 1)}><ChevronLeft size={16}/></button>
    <button type="button" aria-label={t('Next page')} title={t('Next page')} disabled={loading || !page?.hasMore} onClick={() => onPage(page!.index + 1)}><ChevronRight size={16}/></button>
  </nav>
}
