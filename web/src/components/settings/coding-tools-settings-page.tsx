import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import { useI18n } from '@/i18n/i18n'
import { updateUserSettings } from '@/lib/api'
import { CODING_TOOLS, DEFAULT_CODING_PROMPT_TEMPLATE, type CodingToolId } from '@/lib/coding-tools'
import type { BootstrapData, UserSettings } from '@/types/flow'
import { SettingsCrumb, SettingsPageTitle, SettingsRow, SettingsSection, SettingsToggle } from './settings-primitives'
import './welcome-message-settings.css'

const PROMPT_VARIABLES = ['{{issue.identifier}}', '{{issue.title}}', '{{issue.branchName}}', '{{issue.url}}', '{{context}}']

/** Coding tools sub-page: which tools the issue "Work on issue" menu offers, and the prompt they receive. */
export function CodingToolsSettingsPage({
  data,
  onBack,
  onReload,
}: {
  data: BootstrapData
  onBack: () => void
  onReload: () => Promise<void>
}) {
  const { t } = useI18n()
  const settings = data.userSettings?.[data.viewer.id]
  const enabled = new Set(settings?.enabledCodingTools ?? [])
  const [customLink, setCustomLink] = useState(settings?.customDeepLinkUrlTemplate ?? '')
  const [prompt, setPrompt] = useState(settings?.codingPromptTemplate ?? '')
  useEffect(() => {
    setCustomLink(settings?.customDeepLinkUrlTemplate ?? '')
    setPrompt(settings?.codingPromptTemplate ?? '')
  }, [settings?.customDeepLinkUrlTemplate, settings?.codingPromptTemplate])
  const save = async (patch: Partial<UserSettings>) => {
    try {
      await updateUserSettings(patch, data.workspace.urlKey)
      await onReload()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not save coding tools'))
    }
  }
  const toggle = (id: CodingToolId, on: boolean) => {
    const next = CODING_TOOLS.map(tool => tool.id).filter(toolId => (toolId === id ? on : enabled.has(toolId)))
    void save({ enabledCodingTools: next })
  }
  const customLinkInvalid = customLink.trim() !== '' && !/^https?:\/\//i.test(customLink.trim())
  return (
    <div className="welcome-message-page">
      <SettingsCrumb onClick={onBack}>{t('Code & reviews')}</SettingsCrumb>
      <SettingsPageTitle description={t('Choose the coding tools you can open issues in from the issue work menu')}>
        {t('Coding tools')}
      </SettingsPageTitle>
      <SettingsSection title={t('Tools')}>
        {CODING_TOOLS.map(tool => (
          <SettingsRow key={tool.id} title={<span data-i18n-ignore>{tool.name}</span>} description={t(tool.description)}>
            <SettingsToggle label={tool.name} checked={enabled.has(tool.id)} onChange={value => toggle(tool.id, value)} />
          </SettingsRow>
        ))}
      </SettingsSection>
      {enabled.has('customUrl') && (
        <SettingsSection title={t('Custom link')}>
          <div className="welcome-message-editor">
            <input
              aria-label={t('Custom link URL')}
              aria-invalid={customLinkInvalid}
              className="welcome-message-editor__title"
              placeholder="https://example.com/new?prompt={{prompt}}"
              value={customLink}
              onChange={event => setCustomLink(event.target.value)}
              onBlur={() => {
                if (customLinkInvalid) toast.error(t('The link must start with http:// or https://'))
                else if (customLink !== (settings?.customDeepLinkUrlTemplate ?? '')) void save({ customDeepLinkUrlTemplate: customLink.trim() })
              }}
            />
            <p className="welcome-message-editor__meta">
              {t('Use')} <code data-i18n-ignore>{'{{prompt}}'}</code> {t('where the issue prompt should be inserted')}
            </p>
          </div>
        </SettingsSection>
      )}
      <SettingsSection title={t('Prompt template')}>
        <div className="welcome-message-editor">
          <textarea
            aria-label={t('Prompt template')}
            className="welcome-message-editor__content"
            maxLength={8000}
            placeholder={DEFAULT_CODING_PROMPT_TEMPLATE}
            rows={6}
            value={prompt}
            onChange={event => setPrompt(event.target.value)}
            onBlur={() => {
              if (prompt !== (settings?.codingPromptTemplate ?? '')) void save({ codingPromptTemplate: prompt })
            }}
          />
          <p className="welcome-message-editor__meta">
            {t('Used by Copy as prompt and when opening an issue in a coding tool. Variables:')}{' '}
            <span data-i18n-ignore>{PROMPT_VARIABLES.join(' ')}</span>
          </p>
        </div>
      </SettingsSection>
    </div>
  )
}

export default CodingToolsSettingsPage
