import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'

import { useI18n } from '@/i18n/i18n'
import { testWelcomeMessage, updateWorkspacePreferences } from '@/lib/api'
import { inboxPath } from '@/lib/app-routes'
import type { BootstrapData } from '@/types/flow'
import {
  SettingsCrumb,
  SettingsPageTitle,
  SettingsRow,
  SettingsSection,
  SettingsToggle,
} from './settings-primitives'
import './welcome-message-settings.css'

/** Welcome message sub-page: enable, title, content, and a test send. */
export function WelcomeMessageSettingsPage({
  data,
  onBack,
  onReload,
}: {
  data: BootstrapData
  onBack: () => void
  onReload: () => Promise<void>
}) {
  const { t, formatDate } = useI18n()
  const navigate = useNavigate()
  const onOpenInbox = () => navigate(inboxPath(data.workspace.urlKey))
  const settings = data.workspaceSettings
  const [title, setTitle] = useState(settings.welcomeMessageTitle ?? '')
  const [content, setContent] = useState(settings.welcomeMessage ?? '')
  const [sending, setSending] = useState(false)
  useEffect(() => {
    setTitle(settings.welcomeMessageTitle ?? '')
    setContent(settings.welcomeMessage ?? '')
  }, [settings.welcomeMessage, settings.welcomeMessageTitle])
  const save = async (patch: Parameters<typeof updateWorkspacePreferences>[0], success?: string) => {
    try {
      await updateWorkspacePreferences(patch)
      await onReload()
      if (success) toast.success(t(success))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not save welcome message'))
    }
  }
  const editor = data.users.find(user => user.id === settings.welcomeMessageEditedById)
  const sendTest = async () => {
    setSending(true)
    try {
      await testWelcomeMessage()
      await onReload()
      toast.success(t('Test notification sent'), { action: { label: t('Go to inbox'), onClick: onOpenInbox } })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Failed to send test notification'))
    } finally {
      setSending(false)
    }
  }
  return (
    <div className="welcome-message-page">
      <SettingsCrumb onClick={onBack}>{t('Workspace')}</SettingsCrumb>
      <SettingsPageTitle description={t('Configure a message that new users will receive when they join the workspace')}>
        {t('Welcome message')}
      </SettingsPageTitle>
      <SettingsSection>
        <SettingsRow
          title={t('Enable welcome message')}
          description={t('When enabled, new users will receive this message when they join')}
        >
          <SettingsToggle
            label={t('Enable welcome message')}
            checked={Boolean(settings.welcomeMessageEnabled)}
            onChange={value =>
              void save({ welcomeMessageEnabled: value }, value ? 'Welcome message enabled' : 'Welcome message disabled')
            }
          />
        </SettingsRow>
      </SettingsSection>
      <SettingsSection title={t('Welcome message')}>
        <div className="welcome-message-editor">
          <input
            aria-label={t('Welcome message title')}
            className="welcome-message-editor__title"
            maxLength={200}
            placeholder={t('Welcome to {workspace}').replace('{workspace}', data.workspace.name)}
            value={title}
            onChange={event => setTitle(event.target.value)}
            onBlur={() => {
              if (title !== (settings.welcomeMessageTitle ?? '')) void save({ welcomeMessageTitle: title })
            }}
          />
          <textarea
            aria-label={t('Welcome message content')}
            className="welcome-message-editor__content"
            maxLength={4000}
            placeholder={t('Share a greeting, team norms, or links to help new members get started…')}
            rows={8}
            value={content}
            onChange={event => setContent(event.target.value)}
            onBlur={() => {
              if (content !== (settings.welcomeMessage ?? '')) void save({ welcomeMessage: content })
            }}
          />
          {editor && settings.welcomeMessageEditedAt && (
            <p className="welcome-message-editor__meta">
              {t('Edited by')} <span data-i18n-ignore>{editor.displayName}</span> ·{' '}
              {formatDate(settings.welcomeMessageEditedAt, { dateStyle: 'medium' })}
            </p>
          )}
        </div>
      </SettingsSection>
      <SettingsSection title={t('Test notification')}>
        <SettingsRow
          title={t('Send test notification')}
          description={t('Send the welcome message to yourself to see it in your inbox')}
        >
          <button
            className="settings-action"
            type="button"
            disabled={sending || !content.trim()}
            onClick={() => void sendTest()}
          >
            {t('Send test notification')}
          </button>
        </SettingsRow>
      </SettingsSection>
    </div>
  )
}

export default WelcomeMessageSettingsPage
