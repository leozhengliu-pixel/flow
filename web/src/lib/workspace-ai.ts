import type { WorkspaceSettings } from '@/types/flow'

/**
 * Whether the workspace lets content reach the AI model, as the server's workspace AI policy decides it
 * (api/cmd/server/ai_policy.go): HIPAA mode off and the AI switch ("ai-agent", or the older "ai") on. The guest
 * rule depends on the viewer's role; the server refuses those requests (403) and callers fall back.
 */
export function workspaceAIAllowed(settings?: Partial<Pick<WorkspaceSettings, 'featureFlags' | 'hipaaCompliance'>> | null): boolean {
  if (!settings) return true
  if (settings.hipaaCompliance) return false
  return (settings.featureFlags?.['ai-agent'] ?? settings.featureFlags?.ai) !== false
}
