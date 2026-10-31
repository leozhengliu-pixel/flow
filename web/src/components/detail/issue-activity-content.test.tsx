import { fireEvent, render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { IssueActivityContent } from './issue-activity-content'

describe('issue activity content', () => {
  it('keeps the desktop timeline and composer as direct siblings', () => {
    const { container } = render(<IssueActivityContent phone={false} composer={<div className="composer"/>}><div className="timeline"/></IssueActivityContent>)
    expect(container.children).toHaveLength(2)
    expect(container.firstElementChild).toHaveClass('timeline')
    expect(container.lastElementChild).toHaveClass('composer')
    expect(container.querySelector('form')).toBeNull()
  })

  it('groups the phone event stream and composer and prevents native form navigation', () => {
    const { container } = render(<IssueActivityContent phone composer={<div className="composer"/>}><div className="timeline"/></IssueActivityContent>)
    const content = container.querySelector('.issue-activity-mobile')!
    expect(content.firstElementChild).toHaveClass('timeline')
    const form = content.querySelector('form')!
    expect(form.firstElementChild).toHaveClass('composer')
    expect(fireEvent.submit(form)).toBe(false)
  })
})
