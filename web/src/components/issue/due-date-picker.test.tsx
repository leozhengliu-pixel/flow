import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { DueDateCommand } from './due-date-picker'

describe('DueDateCommand', () => {
  it('offers "Remove due date" only when removing is allowed', () => {
    const onSelect = vi.fn().mockResolvedValue(undefined)
    const view = render(<DueDateCommand value="2026-10-10" onSelect={onSelect}/>)
    expect(screen.getByRole('option', { name: /Remove due date/ })).toBeInTheDocument()
    // A recurring issue's due date anchors its schedule: the API refuses to clear it.
    view.rerender(<DueDateCommand value="2026-10-10" allowRemove={false} onSelect={onSelect}/>)
    expect(screen.queryByRole('option', { name: /Remove due date/ })).not.toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Tomorrow/ })).toBeInTheDocument()
  })
})
