/**
 * LS-0687 agentSkillsSettingsBackLink — shared back-link target for skill editor chrome.
 */
import { settingsPath } from '@/lib/app-routes'

export const AGENT_SKILLS_SETTINGS_BACK_LABEL = 'Agent personalization'

/** Personal agents settings page (skills list host). */
export function agentSkillsSettingsBackPage(): 'agents' {
  return 'agents'
}

export function agentSkillsSettingsBackHref(workspaceSlug: string): string {
  return settingsPath(workspaceSlug, 'agents')
}

export function agentSkillsSettingsBackLink(workspaceSlug: string) {
  return {
    href: agentSkillsSettingsBackHref(workspaceSlug),
    label: AGENT_SKILLS_SETTINGS_BACK_LABEL,
    page: agentSkillsSettingsBackPage(),
  }
}
