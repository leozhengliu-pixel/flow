/**
 * LS-0540 SaveCustomViewButtons — dirty Save dropdown: Update view + Create new view…
 */

import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronDown } from 'lucide-react'
import { DropdownMenuContent } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'
import styles from './save-custom-view-buttons.module.css'

export function filtersAreDirty(current: unknown, saved: unknown): boolean {
  return JSON.stringify(current ?? []) !== JSON.stringify(saved ?? [])
}

export function SaveCustomViewButtons({
  canUpdate = true,
  disabledReason,
  saving = false,
  onUpdate,
  onCreate,
}: {
  canUpdate?: boolean
  disabledReason?: string
  saving?: boolean
  onUpdate: () => void
  onCreate: () => void
}) {
  const { t } = useI18n()
  return (
    <div className={styles.strip} data-save-custom-view-buttons>
      <DropdownMenu.Root>
        <div className={styles.group}>
          <button
            type="button"
            className={styles.primary}
            disabled={saving || !canUpdate}
            title={!canUpdate ? (disabledReason ?? t('Cannot merge these filters into the view')) : undefined}
            onClick={() => { if (canUpdate && !saving) onUpdate() }}
          >
            {saving ? t('Saving…') : t('Save')}
          </button>
          <DropdownMenu.Trigger asChild>
            <button
              type="button"
              className={styles.chevron}
              aria-label={t('Save view options')}
              disabled={saving}
            >
              <ChevronDown size={12} />
            </button>
          </DropdownMenu.Trigger>
        </div>
        <DropdownMenu.Portal>
          <DropdownMenuContent align="end" className={styles.menu} sideOffset={4} data-flow-motion="floating">
            <DropdownMenu.Item
              disabled={!canUpdate}
              title={!canUpdate ? (disabledReason ?? t('Cannot merge these filters into the view')) : undefined}
              onSelect={() => { if (canUpdate) onUpdate() }}
            >
              {t('Update view')}
            </DropdownMenu.Item>
            <DropdownMenu.Item onSelect={() => onCreate()}>
              {t('Create new view…')}
            </DropdownMenu.Item>
          </DropdownMenuContent>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </div>
  )
}
