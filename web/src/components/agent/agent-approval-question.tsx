import type { AgentMessagePart, BootstrapData } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { AgentAnswerText, AgentChoicePills } from './agent-answer'
import { agentApprovalChoices, agentApprovalMarkdown } from './agent-approval-model'

type Choice = { label: string; value: string }

/**
 * A question the agent stopped on (ask_user) or an approval it asked for, with its option pills. The chosen pill stays
 * with a check once answered's select elicitation.
 */
export function AgentQuestionPrompt({ part, data, className, ariaLabel, disabled, onSelect }: { part: AgentMessagePart; data?: BootstrapData; className: string; ariaLabel?: string; disabled: boolean; onSelect?: (choice: Choice) => void }) {
  const { t } = useI18n()
  const question = part.question
  if (!question) return null
  const approval = question.approval
  const markdown = approval ? agentApprovalMarkdown(approval, t) : part.text ?? ''
  const choices = approval ? agentApprovalChoices(question.options, t) : question.options
  return <>
    {markdown && <AgentAnswerText ariaLabel={ariaLabel} className={className} data={data} markdown={markdown}/>}
    <AgentChoicePills
      choices={choices}
      disabled={disabled}
      onSelect={onSelect}
      selected={part.status === 'pending' ? undefined : question.selected ?? (approval?.decision === 'approved' ? 'approve' : approval?.decision === 'rejected' ? 'reject' : '')}
    />
  </>
}
