import { beforeEach, describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { ClientStorage } from '@/lib/client-storage'
import {
  AiFeedbackControls,
  hasNegativeFeedback,
  hasPositiveFeedback,
  readAiMessageFeedback,
  writeAiMessageFeedback,
} from './ai-feedback-controls'

describe('AiFeedbackControls (LS-0049)', () => {
  beforeEach(() => {
    for (const key of ClientStorage.getKeys('local')) {
      if (key.startsWith('flow:ai-feedback:')) ClientStorage.remove(key, 'local')
    }
  })

  it('persists positive and negative feedback per user', () => {
    writeAiMessageFeedback('m1', { positiveUserIds: ['u1'], negativeUserIds: [] })
    const feedback = readAiMessageFeedback('m1')
    expect(hasPositiveFeedback(feedback, 'u1')).toBe(true)
    expect(hasNegativeFeedback(feedback, 'u2')).toBe(false)
  })

  it('toggles thumbs and clears the opposite vote', () => {
    render(<AiFeedbackControls messageId="msg-9" userId="viewer" />)
    const up = screen.getByLabelText('This was helpful')
    const down = screen.getByLabelText('Leave feedback…')
    fireEvent.click(up)
    expect(hasPositiveFeedback(readAiMessageFeedback('msg-9'), 'viewer')).toBe(true)
    fireEvent.click(down)
    const next = readAiMessageFeedback('msg-9')
    expect(hasPositiveFeedback(next, 'viewer')).toBe(false)
    expect(hasNegativeFeedback(next, 'viewer')).toBe(true)
    fireEvent.click(down)
    expect(hasNegativeFeedback(readAiMessageFeedback('msg-9'), 'viewer')).toBe(false)
  })
})
