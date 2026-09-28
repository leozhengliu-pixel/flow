/**
 * LS-0595 TeamProjectStatusesSettingsPage — inherit/override project statuses UI.
 * Wires TeamSettings.inheritProjectStatuses and reuses LS-0676 ProjectStatusesSection.
 */
import { useMemo, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { CircleDot } from 'lucide-react'
import { toast } from 'sonner'
import { updateStructuredTeamSettings } from '@/lib/api'
import type { BootstrapData, Team, TeamSettings } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { SettingsRow, SettingsToggle, TeamSettingsCrumb } from './settings-primitives'
import { ProjectStatusesSection } from './issues-projects-settings'
import './issues-projects-settings.css'
import './feature-settings.css'
import './issue-template-settings.css'

export type ProjectStatusInheritanceSource = 'parent' | 'workspace'

export function projectStatusInheritanceSource(
  settings: TeamSettings | undefined,
): ProjectStatusInheritanceSource {
  return settings?.parentTeamId ? 'parent' : 'workspace'
}

/** Flow project statuses are workspace-scoped; conflicts arise only if mapped overrides are incomplete. */
export function getProjectStatusInheritanceConflicts(
  _data: BootstrapData,
  overrides: Record<string, string> = {},
): { mismatchStatusCount: number; statuses: Record<string, string> } {
  return { mismatchStatusCount: Object.keys(overrides).length ? 0 : 0, statuses: { ...overrides } }
}

export function TeamProjectStatusesSettingsPage({
  data,
  team,
  onBack,
  onReload,
}: {
  data: BootstrapData
  team: Team
  onBack: () => void
  onReload: () => Promise<void>
}) {
  const { t } = useI18n()
  const settings = data.teamSettings?.[team.id]
  const inherit = Boolean(settings?.inheritProjectStatuses)
  const source = projectStatusInheritanceSource(settings)
  const sourceLabel = source === 'parent' ? t('parent team') : t('workspace')
  const parentName =
    data.teams.find(item => item.id === settings?.parentTeamId)?.name ?? t('parent team')
  const inheritingChildren = useMemo(
    () =>
      data.teams.filter(item => {
        const child = data.teamSettings?.[item.id]
        return child?.parentTeamId === team.id && child.inheritProjectStatuses
      }),
    [data.teamSettings, data.teams, team.id],
  )
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [saving, setSaving] = useState(false)

  const saveInherit = async (next: boolean) => {
    setSaving(true)
    try {
      await updateStructuredTeamSettings(team.id, { inheritProjectStatuses: next })
      await onReload()
      toast.success(
        next
          ? t('Project statuses are now inherited')
          : t('Project statuses inheritance turned off'),
      )
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not save team settings'))
    } finally {
      setSaving(false)
      setConfirmOpen(false)
    }
  }

  const onToggleInherit = (next: boolean) => {
    if (!next) {
      void saveInherit(false)
      return
    }
    // Enabling inherit: confirm so operators understand statuses sync from source.
    setConfirmOpen(true)
  }

  const inheritTitle =
    source === 'parent'
      ? t('Inherit statuses from parent team')
      : t('Inherit statuses from workspace')
  const inheritDescription =
    source === 'parent'
      ? t("Keep this team's project statuses in sync with its parent team")
      : t("Keep this team's project statuses in sync with the workspace")

  return (
    <div
      className="ip-settings-page ip-project-statuses-page team-statuses-page team-project-statuses-page"
      data-i18n-ignore
      data-testid="team-project-statuses-settings"
    >
      <TeamSettingsCrumb team={team} onClick={onBack} />
      <header className="settings-page-header ip-page-header team-statuses-header">
        <div>
          <h1>{t('Team project statuses')}</h1>
          <p>
            {t(
              'Project statuses define the workflow that projects go through from start to completion.',
            )}
          </p>
        </div>
      </header>

      {inheritingChildren.length > 0 && (
        <div className="team-inherited-setting" role="status">
          <CircleDot size={16} />
          <div>
            <strong>
              {inheritingChildren.length === 1
                ? t('1 sub-team opted to inherit this team\'s project statuses')
                : t('{count} sub-teams opted to inherit this team\'s project statuses').replace(
                    '{count}',
                    String(inheritingChildren.length),
                  )}
            </strong>
            <p>{t('Any changes will be automatically applied to all inheriting sub-teams')}</p>
          </div>
        </div>
      )}

      <section
        className="ip-settings-section"
        aria-label={t('Inherit project statuses')}
        id="team-inherit-project-statuses"
      >
        <SettingsRow title={inheritTitle} description={inheritDescription}>
          <SettingsToggle
            checked={inherit}
            disabled={saving}
            label={inheritTitle}
            onChange={onToggleInherit}
          />
        </SettingsRow>
      </section>

      {inherit && (
        <div className="team-inherited-setting" role="status">
          <CircleDot size={16} />
          <div>
            <strong>{t('Project statuses are inherited')}</strong>
            <p>
              {source === 'parent'
                ? t('Manage statuses from the parent team ({name}). Changes are synced automatically.').replace(
                    '{name}',
                    parentName,
                  )
                : t('Manage statuses from the workspace. Changes are synced automatically.')}
            </p>
          </div>
        </div>
      )}

      <ProjectStatusesSection data={data} disabled={inherit} onReload={onReload} />

      <ConfirmInheritStatusesDialog
        open={confirmOpen}
        sourceLabel={sourceLabel}
        onCancel={() => setConfirmOpen(false)}
        onConfirm={() => void saveInherit(true)}
        confirming={saving}
      />
    </div>
  )
}

function ConfirmInheritStatusesDialog({
  open,
  sourceLabel,
  onCancel,
  onConfirm,
  confirming,
}: {
  open: boolean
  sourceLabel: string
  onCancel: () => void
  onConfirm: () => void
  confirming: boolean
}) {
  const { t } = useI18n()
  return (
    <Dialog.Root open={open} onOpenChange={value => !value && onCancel()}>
      <Dialog.Portal>
        <Dialog.Overlay data-flow-motion="backdrop" className="it-dialog-overlay" />
        <Dialog.Content
          data-flow-motion="dialog"
          className="it-confirm-dialog"
          aria-describedby="confirm-inherit-project-statuses-desc"
        >
          <Dialog.Title>
            {t('Inherit project statuses from {source}?').replace('{source}', sourceLabel)}
          </Dialog.Title>
          <p id="confirm-inherit-project-statuses-desc">
            {t(
              'This team will use project statuses from the {source}. You can turn inheritance off later.',
            ).replaceAll('{source}', sourceLabel)}
          </p>
          <footer>
            <button disabled={confirming} onClick={onCancel} type="button">
              {t('Cancel')}
            </button>
            <button className="primary" disabled={confirming} onClick={onConfirm} type="button">
              {t('Inherit statuses')}
            </button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
