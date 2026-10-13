import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { FlowTooltip, TOOLTIP_DELAY, TOOLTIP_OFFSET, TOOLTIP_SKIP_DELAY, TooltipProvider } from './tooltip'
import { tooltipShortcutKeys } from './tooltip-shortcut'

function renderTooltip(props: Partial<Parameters<typeof FlowTooltip>[0]> = {}, wrapper?: (node: React.ReactElement) => React.ReactElement) {
  const button = <FlowTooltip label="Copy issue URL" shortcut="⌘ ⇧ ," {...props}><button type="button">Copy</button></FlowTooltip>
  return render(<TooltipProvider delayDuration={0}>{wrapper ? wrapper(button) : button}</TooltipProvider>)
}

describe('FlowTooltip', () => {
  it('uses the reference timing and offset', () => {
    expect([TOOLTIP_DELAY, TOOLTIP_SKIP_DELAY, TOOLTIP_OFFSET]).toEqual([450, 400, 8])
  })

  it('splits shortcuts into boxed keys and "then" steps', () => {
    expect(tooltipShortcutKeys('⌘ ⇧ ,')).toEqual([['⌘', '⇧', ',']])
    expect(tooltipShortcutKeys('⌘⇧U')).toEqual([['⌘', '⇧', 'U']])
    expect(tooltipShortcutKeys('Ctrl+Enter')).toEqual([['Ctrl', 'Enter']])
    expect(tooltipShortcutKeys('W then O')).toEqual([['W'], ['O']])
    expect(tooltipShortcutKeys('M, then R')).toEqual([['M'], ['R']])
    expect(tooltipShortcutKeys('Esc')).toEqual([['Esc']])
  })

  it('renders the label and one key cap per key on the shared surface', async () => {
    renderTooltip()
    await userEvent.hover(screen.getByRole('button', { name: 'Copy' }))
    const surface = (await screen.findAllByText('Copy issue URL'))[0].closest('.flow-tooltip-content')!
    expect(surface).not.toHaveClass('flow-tooltip-content--elevated')
    expect([...surface.querySelectorAll('kbd.flow-tooltip-key')].map(key => key.textContent)).toEqual(['⌘', '⇧', ','])
    expect(surface.querySelector('.flow-tooltip-then')).toBeNull()
  })

  it('puts a plain "then" between sequence steps', async () => {
    renderTooltip({ label: 'Work on issue', shortcut: 'W then O' })
    await userEvent.hover(screen.getByRole('button', { name: 'Copy' }))
    const surface = (await screen.findAllByText('Work on issue'))[0].closest('.flow-tooltip-content')!
    const shortcut = surface.querySelector('.flow-tooltip-shortcut')!
    expect([...shortcut.children].map(node => `${node.tagName}:${node.textContent}`)).toEqual(['KBD:W', 'SPAN:then', 'KBD:O'])
  })

  it('uses the elevated palette over a surface marked data-tooltip-elevated', async () => {
    renderTooltip({ label: 'Send message', shortcut: 'Enter' }, node => <div data-tooltip-elevated>{node}</div>)
    await userEvent.hover(screen.getByRole('button', { name: 'Copy' }))
    const surface = (await screen.findAllByText('Send message'))[0].closest('.flow-tooltip-content')!
    expect(surface).toHaveClass('flow-tooltip-content--elevated')
  })

  it('renders nothing extra without a label or shortcut', () => {
    render(<TooltipProvider><FlowTooltip><button type="button">Plain</button></FlowTooltip></TooltipProvider>)
    expect(screen.getByRole('button', { name: 'Plain' })).not.toHaveAttribute('data-state')
  })
})
