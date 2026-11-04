import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { AssigneeHoverPreview } from './assignee-hover-card'
import { PeopleProvider } from './people-provider'

function card(active = true) {
  const data = makeBootstrap()
  const person = { ...data.viewer, displayName: '张伟', username: 'zhang.wei', active, outOfOfficeUntil: '2099-11-12T00:00:00Z' }
  return render(<I18nProvider><PeopleProvider workspaceKey="acme" users={[person]} teams={data.teams} teamMembers={[{ teamId: data.teams[0].id, userId: person.id, role: 'member', joinedAt: '' }]}><AssigneeHoverPreview user={person}/></PeopleProvider></I18nProvider>)
}

describe('assignee person card', () => {
  it('uses the live directory only while presence is known', () => {
    const user = makeBootstrap().viewer
    const shell = (known: boolean) => <I18nProvider><PeopleProvider users={[user]} presence={known ? [] : undefined}><AssigneeHoverPreview user={user}/></PeopleProvider></I18nProvider>
    const { rerender } = render(shell(true))
    expect(screen.getByText('Offline')).toBeVisible()
    rerender(shell(false))
    expect(screen.queryByText('Offline')).not.toBeInTheDocument()
  })

  it('links the visible profile and teams, and shows real availability without email or fabricated presence', () => {
    card()
    expect(screen.getByRole('link', { name: '张伟' })).toHaveAttribute('href', '/acme/profiles/zhang.wei')
    expect(screen.getAllByRole('link')).toHaveLength(2)
    expect(screen.getByText('Out of office')).toBeVisible()
    expect(screen.queryByText('Online')).not.toBeInTheDocument()
    expect(screen.queryByText('Offline')).not.toBeInTheDocument()
    expect(screen.queryByText('Email')).not.toBeInTheDocument()
  })

  it('keeps an inactive person identifiable without profile links, stale presence or availability', () => {
    card(false)
    expect(screen.getByText('张伟')).toBeVisible()
    expect(screen.getByText('Inactive')).toBeVisible()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.queryByText('Out of office')).not.toBeInTheDocument()
  })
})
