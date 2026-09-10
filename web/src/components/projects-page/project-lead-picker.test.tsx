import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { VirtuosoMockContext } from 'react-virtuoso'
import { I18nProvider } from '@/i18n/i18n'
import { PeopleProvider } from '@/components/property/people-provider'
import { viewer, teammate } from '@/test/fixtures'
import type { TeamMember, User } from '@/types/flow'
import { ProjectLeadPicker } from './project-lead-picker'
import { ProjectPropertyPicker } from './project-property-picker'
import { ProjectFilterValues } from './projects-page-surface'

beforeEach(()=>localStorage.setItem('flow:locale','en-US'))

it('uses the shared personnel picker with project-team suggestions, search, and clearing',async()=>{
  const change=vi.fn(),invite=vi.fn()
  const teamMembers=[{teamId:'team-1',userId:viewer.id}] as TeamMember[]
  render(<I18nProvider><PeopleProvider users={[viewer,teammate]} teamMembers={teamMembers}><ProjectLeadPicker value={viewer} users={[viewer,teammate]} teamIds={['team-1']} onChange={change} onInvite={invite}/></PeopleProvider></I18nProvider>)
  fireEvent.click(screen.getByRole('combobox',{name:/Change Lead/}))
  const menu=screen.getByRole('dialog')
  expect(menu).toHaveClass('property-command-surface')
  expect(within(menu).queryByRole('option',{name:'Teammate'})).not.toBeInTheDocument()
  fireEvent.change(within(menu).getByRole('textbox'),{target:{value:teammate.email}})
  fireEvent.keyDown(within(menu).getByRole('textbox'),{key:'Enter'})
  expect(change).toHaveBeenCalledWith(teammate.id)
  fireEvent.click(screen.getByRole('combobox',{name:/Change Lead/}))
  fireEvent.click(screen.getByRole('option',{name:/^No lead/}))
  expect(change).toHaveBeenCalledWith('')
  fireEvent.click(screen.getByRole('combobox',{name:/Change Lead/}))
  fireEvent.click(screen.getByRole('option',{name:/Invite and add/}))
  expect(invite).toHaveBeenCalledOnce()
  expect(change).toHaveBeenCalledTimes(2)
})

it('uses the directory avatar when a project row only supplies a lead ID and name',()=>{
  const user={...viewer,avatarUrl:'https://example.test/lead.png'}
  render(<I18nProvider><PeopleProvider users={[user]}><ProjectPropertyPicker label="Change lead" property="lead" value={user.id} options={[{value:user.id,label:user.displayName}]} onChange={vi.fn()}>Lead</ProjectPropertyPicker></PeopleProvider></I18nProvider>)
  fireEvent.click(screen.getByRole('combobox',{name:'Change lead'}))
  expect(screen.getByRole('option',{name:user.displayName}).querySelector('img')).toHaveAttribute('src',user.avatarUrl)
})

it('shares personnel rendering for lead filters while retaining project counts and bounded rows',async()=>{
  const users=Array.from({length:10000},(_,i)=>({...viewer,id:`u-${i}`,displayName:`Person ${i}`,email:`person${i}@example.test`,avatarUrl:i===9999?'https://example.test/avatar.png':undefined} as User))
  const select=vi.fn()
  render(<I18nProvider><PeopleProvider users={users}><VirtuosoMockContext.Provider value={{viewportHeight:320,itemHeight:32}}><ProjectFilterValues field="Lead" options={[{id:'',label:'No lead',count:0},...users.map(user=>({id:user.id,label:user.displayName,count:2}))]} onSelect={select}/></VirtuosoMockContext.Provider></PeopleProvider></I18nProvider>)
  await waitFor(()=>expect(screen.getAllByRole('option').length).toBeGreaterThan(0))
  expect(screen.getAllByRole('option').length).toBeLessThan(30)
  fireEvent.change(screen.getByRole('textbox'),{target:{value:'person9999@example.test'}})
  const row=screen.getByRole('option',{name:/Person 9999/})
  expect(row).toHaveTextContent('2 projects')
  expect(row.querySelector('img')).toHaveAttribute('src','https://example.test/avatar.png')
  fireEvent.keyDown(screen.getByRole('textbox'),{key:'Enter'})
  expect(select).toHaveBeenCalledWith({id:'u-9999',label:'Person 9999',count:2})
})
