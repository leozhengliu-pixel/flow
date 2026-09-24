/**
 * LS-0049 AiFeedbackControls — thumbs up/down on agent / AI messages.
 * Feedback is keyed by message (or part) id and persisted via ClientStorage
 * (REST-friendly; no GraphQL). Shape mirrors Linear metadata.feedback.
 */
import { useCallback, useMemo, useState } from 'react'
import { ThumbsDown, ThumbsUp } from 'lucide-react'
import { ClientStorage } from '@/lib/client-storage'
import './ai-feedback-controls.css'

export type AiMessageFeedback = {
  positiveUserIds: string[]
  negativeUserIds: string[]
}

export type AiFeedbackControlsProps = {
  messageId: string
  partId?: string
  userId: string
  variant?: 'default' | 'compact'
  size?: 'small' | 'medium'
  'aria-label'?: string
  className?: string
  onChange?: (feedback: AiMessageFeedback) => void
}

const STORAGE_PREFIX = 'flow:ai-feedback:'

function storageKey(messageId: string, partId?: string) {
  return `${STORAGE_PREFIX}${messageId}${partId ? `:${partId}` : ''}`
}

export function readAiMessageFeedback(messageId: string, partId?: string): AiMessageFeedback {
  const stored = ClientStorage.get<AiMessageFeedback>(storageKey(messageId, partId), {
    storageMechanism: 'local',
    logError: false,
  })
  return {
    positiveUserIds: stored?.positiveUserIds ?? [],
    negativeUserIds: stored?.negativeUserIds ?? [],
  }
}

export function writeAiMessageFeedback(
  messageId: string,
  feedback: AiMessageFeedback,
  partId?: string,
) {
  ClientStorage.set(storageKey(messageId, partId), feedback, 'local')
}

export function hasPositiveFeedback(feedback: AiMessageFeedback, userId: string) {
  return feedback.positiveUserIds.includes(userId)
}

export function hasNegativeFeedback(feedback: AiMessageFeedback, userId: string) {
  return feedback.negativeUserIds.includes(userId)
}

function toggleId(ids: string[], userId: string, enabled: boolean) {
  const next = ids.filter(id => id !== userId)
  if (enabled) next.push(userId)
  return next
}

export function AiFeedbackControls({
  messageId,
  partId,
  userId,
  variant = 'default',
  size = 'small',
  'aria-label': ariaLabel,
  className,
  onChange,
}: AiFeedbackControlsProps) {
  const initial = useMemo(() => readAiMessageFeedback(messageId, partId), [messageId, partId])
  const [feedback, setFeedback] = useState<AiMessageFeedback>(initial)

  const positive = hasPositiveFeedback(feedback, userId)
  const negative = hasNegativeFeedback(feedback, userId)

  const commit = useCallback(
    (next: AiMessageFeedback) => {
      setFeedback(next)
      writeAiMessageFeedback(messageId, next, partId)
      onChange?.(next)
    },
    [messageId, onChange, partId],
  )

  const onPositive = () => {
    if (positive) {
      commit({
        ...feedback,
        positiveUserIds: toggleId(feedback.positiveUserIds, userId, false),
      })
      return
    }
    commit({
      positiveUserIds: toggleId(feedback.positiveUserIds, userId, true),
      negativeUserIds: toggleId(feedback.negativeUserIds, userId, false),
    })
  }

  const onNegative = () => {
    if (negative) {
      commit({
        ...feedback,
        negativeUserIds: toggleId(feedback.negativeUserIds, userId, false),
      })
      return
    }
    commit({
      positiveUserIds: toggleId(feedback.positiveUserIds, userId, false),
      negativeUserIds: toggleId(feedback.negativeUserIds, userId, true),
    })
  }

  const positiveLabel = positive ? 'Remove feedback' : 'This was helpful'
  const negativeLabel = negative ? 'Remove feedback' : (ariaLabel ?? 'Leave feedback…')

  return (
    <div
      className={[
        'ai-feedback-controls',
        variant === 'compact' ? 'is-compact' : '',
        `is-${size}`,
        className,
      ]
        .filter(Boolean)
        .join(' ')}
      data-surface="LS-0049"
      role="group"
      aria-label="Message feedback"
    >
      <button
        aria-label={positiveLabel}
        aria-pressed={positive}
        className={positive ? 'is-active' : undefined}
        onClick={onPositive}
        title={positiveLabel}
        type="button"
      >
        <ThumbsUp size={size === 'medium' ? 15 : 13} />
      </button>
      <button
        aria-label={negativeLabel}
        aria-pressed={negative}
        className={negative ? 'is-active' : undefined}
        onClick={onNegative}
        title={negativeLabel}
        type="button"
      >
        <ThumbsDown size={size === 'medium' ? 15 : 13} />
      </button>
    </div>
  )
}

AiFeedbackControls.displayName = 'AiFeedbackControls'
