import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, project } from '@/test/fixtures'
import { IssueProjectPicker } from './issue-project-picker'

describe('issue project list search', () => {
  it.each([1,9])('keeps %i projects searchable and uses a compact empty query only for short lists', async count => {
    const user = userEvent.setup()
    const projects = Array.from({length:count},(_,index)=>({...project,id:`project-${index}`,name:`Project ${index}`}))
    render(<I18nProvider><IssueProjectPicker issue={makeIssue({project:undefined})} data={makeBootstrap({projects,teamSettings:{},projectTemplates:[],users:[],invitations:[],agentSkills:[]})} onUpdate={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('combobox',{name:'Add to project'}))
    const menu = screen.getByRole('dialog',{name:'Change Project'})
    const input = within(menu).getByRole('textbox')
    expect(input.parentElement?.classList.contains('is-visually-hidden')).toBe(count<=8)
    await user.type(input,'Project 0')
    expect(input.parentElement).not.toHaveClass('is-visually-hidden')
    expect(within(menu).getByRole('option',{name:'Project 0'})).toBeInTheDocument()
  })
})
