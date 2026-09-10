import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { VirtuosoMockContext } from 'react-virtuoso'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import type { User } from '@/types/flow'
import { PeopleMenuItems } from './people-menu-items'

const users=Array.from({length:10000},(_,i)=>({id:`user-${i}`,userId:`EMP-${String(i).padStart(5,'0')}`,name:`User ${i}`,displayName:`Person ${String(i).padStart(5,'0')}`,email:`person${i}@example.test`,active:true} as User))
const wrapper=({children}:{children:React.ReactNode})=><I18nProvider><VirtuosoMockContext.Provider value={{viewportHeight:320,itemHeight:32}}>{children}</VirtuosoMockContext.Provider></I18nProvider>

it('bounds personnel dropdown rows and retains normalized employee-ID search',async()=>{
  const select=vi.fn()
  render(<DropdownMenu.Root defaultOpen><DropdownMenu.Trigger>Owner</DropdownMenu.Trigger><DropdownMenu.Content><PeopleMenuItems users={users} selectedId="user-0" onSelect={select}/></DropdownMenu.Content></DropdownMenu.Root>,{wrapper})
  await waitFor(()=>expect(screen.getAllByRole('menuitem').length).toBeGreaterThan(0))
  expect(screen.getAllByRole('menuitem').length).toBeLessThan(30)
  fireEvent.change(screen.getByRole('textbox'),{target:{value:'EMP09999'}})
  expect(await screen.findByRole('menuitem',{name:'Person 09999'})).toBeInTheDocument()
  fireEvent.keyDown(screen.getByRole('textbox'),{key:'Enter'})
  expect(select).toHaveBeenCalledWith('user-9999')
})

it('virtualizes member selection while keeping empty, group and multi-select semantics',async()=>{
  const select=vi.fn()
  render(<PersonPicker ariaLabel="Members" emptyTriggerLabel="Members" label="Members" multiple selectedIds={['user-0']} people={users.map(user=>({...user,label:user.displayName}))} searchPlaceholder="Search members" triggerClassName="members-trigger" unselectedGroupLabel="Other users" onChange={select}/>,{wrapper})
  fireEvent.click(screen.getByRole('combobox',{name:'Members'}))
  await waitFor(()=>expect(screen.getAllByRole('option').length).toBeGreaterThan(0))
  expect(screen.getAllByRole('option').length).toBeLessThan(30)
  expect(screen.getByRole('option',{name:'Person 00000'})).toHaveAttribute('aria-checked','true')
  fireEvent.change(screen.getByRole('textbox'),{target:{value:'person9999@example.test'}})
  fireEvent.keyDown(screen.getByRole('textbox'),{key:'Enter'})
  expect(select).toHaveBeenCalledWith('user-9999')
  expect(screen.getByRole('dialog')).toBeInTheDocument()
})
