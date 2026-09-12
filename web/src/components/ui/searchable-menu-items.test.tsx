import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { VirtuosoMockContext } from 'react-virtuoso'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { SearchableMenuItems } from './searchable-menu-items'

it('keeps a 10,000-option menu bounded and can search/select the final option', async () => {
  const options = Array.from({length:10000},(_,i)=>({id:String(i),label:`Team ${String(i).padStart(5,'0')}`,entity:true}))
  const selected = vi.fn()
  render(<I18nProvider><VirtuosoMockContext.Provider value={{viewportHeight:320,itemHeight:32}}><DropdownMenu.Root defaultOpen><DropdownMenu.Trigger>Teams</DropdownMenu.Trigger><DropdownMenu.Content><SearchableMenuItems options={options} renderOption={option=><span data-menu-label>{option.label}</span>} onSelect={selected}/></DropdownMenu.Content></DropdownMenu.Root></VirtuosoMockContext.Provider></I18nProvider>)
  await waitFor(()=>expect(screen.getAllByRole('menuitem').length).toBeGreaterThan(0))
  expect(screen.getAllByRole('menuitem').length).toBeLessThan(30)
  fireEvent.change(screen.getByRole('textbox'),{target:{value:'Team 09999'}})
  expect(await screen.findByRole('menuitem',{name:'Team 09999'})).toBeInTheDocument()
  fireEvent.keyDown(screen.getByRole('textbox'),{key:'Enter'})
  expect(selected).toHaveBeenCalledWith(options[9999])
  await waitFor(()=>expect(screen.queryByRole('menu')).not.toBeInTheDocument())
})

it('moves selection across search results without Radix typeahead stealing the search input', async () => {
  const selected=vi.fn()
  render(<I18nProvider><DropdownMenu.Root defaultOpen><DropdownMenu.Trigger>Teams</DropdownMenu.Trigger><DropdownMenu.Content><SearchableMenuItems options={[{id:'one',label:'Alpha'},{id:'two',label:'Beta'}]} renderOption={option=><span>{option.label}</span>} onSelect={selected}/></DropdownMenu.Content></DropdownMenu.Root></I18nProvider>)
  const search=screen.getByRole('textbox')
  await waitFor(()=>expect(search).toHaveFocus())
  fireEvent.keyDown(search,{key:'ArrowDown'})
  fireEvent.keyDown(search,{key:'Enter'})
  expect(selected).toHaveBeenCalledWith({id:'two',label:'Beta'})
})

it('offers creating a workspace label when the search has no match', async () => {
  const selected = vi.fn()
  const created = vi.fn()
  render(<I18nProvider><DropdownMenu.Root defaultOpen><DropdownMenu.Trigger>Labels</DropdownMenu.Trigger><DropdownMenu.Content><SearchableMenuItems options={[{id:'one',label:'Launch'}]} renderOption={option=><span>{option.label}</span>} onSelect={selected} onCreate={created}/></DropdownMenu.Content></DropdownMenu.Root></I18nProvider>)
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'test' } })
  const create = await screen.findByRole('menuitem', { name: /Create new workspace label.*test/ })
  expect(screen.queryByText('No results')).not.toBeInTheDocument()
  fireEvent.click(create)
  expect(created).toHaveBeenCalledWith('test')
  expect(selected).not.toHaveBeenCalled()
})
