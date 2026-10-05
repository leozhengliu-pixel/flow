import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronDown, X } from 'lucide-react'
import { DropdownMenuContent } from '@/components/ui/dropdown-menu'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import styles from './saved-view-filter-band.module.css'

/**
 * Right side of Linear's filter band on a saved view with unsaved filters: "Clear" (⌥⇧F) and a
 * "Save ⌄" pill with "Save to this view" (⌥S) and "Create new view…" (⌥V).
 */
export function SavedViewBandCommands({ canUpdate = true, saving = false, onClear, onCreate, onUpdate }: { canUpdate?: boolean; saving?: boolean; onClear: () => void; onCreate: () => void; onUpdate: () => void }) {
  const { t } = useI18n()
  return <div className={styles.commands} data-saved-view-band-commands="">
    <ScopedFlowTooltip label={t('Clear all filters')} shortcut="⌥ ⇧ F"><button type="button" className={styles.clear} aria-label={t('Clear all filters')} onClick={onClear}>{t('Clear')}</button></ScopedFlowTooltip>
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button type="button" className={styles.save} disabled={saving} aria-label={t('Save view options')}><span>{t(saving ? 'Saving...' : 'Save')}</span><ChevronDown size={12}/></button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenuContent align="end" className={styles.menu} sideOffset={4} data-flow-motion="floating">
          <DropdownMenu.Item className={styles.item} disabled={!canUpdate} onSelect={onUpdate}><span>{t('Save to this view')}</span><kbd>⌥ S</kbd></DropdownMenu.Item>
          <DropdownMenu.Item className={styles.item} onSelect={onCreate}><span>{t('Create new view…')}</span><kbd>⌥ V</kbd></DropdownMenu.Item>
        </DropdownMenuContent>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  </div>
}

/** Linear's list footer while temporary filters hide part of a saved view. */
export function HiddenByFiltersFooter({ hidden, onClear }: { hidden: number; onClear: () => void }) {
  const { t } = useI18n()
  if (hidden <= 0) return null
  return <div className={styles.footer} role="status">
    <span><b>{t(hidden === 1 ? '1 issue' : '{count} issues').replace('{count}', String(hidden))}</b> {t('hidden by filters')}</span>
    <button type="button" className={styles.footerClear} onClick={onClear}><span>{t('Clear Filters')}</span><X size={12}/></button>
  </div>
}

/** Linear's empty list while filters match nothing. */
export function NoMatchingIssues() {
  const { t } = useI18n()
  return <div className={styles.noMatches} role="status">
    <svg aria-hidden="true" className={styles.noMatchesArt} viewBox="0 0 64 48" fill="none">
      <rect x="6" y="6" width="40" height="30" rx="5" stroke="currentColor" strokeWidth="1.5" strokeDasharray="3 3"/>
      <path d="M14 15h24M14 21h16M14 27h20" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
      <circle cx="46" cy="32" r="8" fill="var(--theme-surface-0)" stroke="currentColor" strokeWidth="1.5"/>
      <path d="m52 38 5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    </svg>
    <strong>{t('No matching issues')}</strong>
  </div>
}
