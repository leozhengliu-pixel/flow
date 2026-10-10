import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { LinearContextMenuPortal, LinearContextMenuRoot, LinearContextMenuTrigger, LinearMenuContent, LinearMenuItem, LinearMenuShortcut, LinearSubmenu } from './row-context-menu'
import { LinearReminderOptions } from './reminder-options'
import { chordMatches, createShortcutMatcher, parseShortcut, shortcutSteps, useLinearRowShortcuts } from './menu-shortcuts'

const key = (init: Partial<KeyboardEvent> & { key: string; code: string }) => ({ metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...init })

describe('Linear menu key hints', () => {
  it('parses sequences and chords in Linear notation', () => {
    expect(parseShortcut('P then S')).toEqual([{ key: 'p', meta: false, ctrl: false, alt: false, shift: false }, { key: 's', meta: false, ctrl: false, alt: false, shift: false }])
    expect(parseShortcut('Ctrl ⌥ D')).toEqual([{ key: 'd', meta: false, ctrl: true, alt: true, shift: false }])
    expect(parseShortcut('⌘ ⇧ ,')).toEqual([{ key: ',', meta: true, ctrl: false, alt: false, shift: true }])
  })

  it('prints macOS glyphs on a Mac and words elsewhere', () => {
    expect(shortcutSteps('Ctrl ⌥ D', true)).toEqual([['Ctrl', '⌥', 'D']])
    expect(shortcutSteps('Ctrl ⌥ D', false)).toEqual([['Ctrl', 'Alt', 'D']])
    expect(shortcutSteps('⌘ ⇧ ,', false)).toEqual([['Ctrl', 'Shift', ',']])
    expect(shortcutSteps('P then S', true)).toEqual([['P'], ['S']])
  })

  it('matches chords by physical key, with ⌘ as Ctrl off macOS', () => {
    expect(chordMatches(key({ key: 'ƒ', code: 'KeyF', altKey: true }), parseShortcut('⌥ F')[0], true)).toBe(true)
    expect(chordMatches(key({ key: 'f', code: 'KeyF' }), parseShortcut('⌥ F')[0], true)).toBe(false)
    expect(chordMatches(key({ key: '.', code: 'Period', ctrlKey: true }), parseShortcut('⌘ .')[0], false)).toBe(true)
    expect(chordMatches(key({ key: '.', code: 'Period', metaKey: true }), parseShortcut('⌘ .')[0], true)).toBe(true)
  })

  it('completes "X then Y" sequences within the timeout only', () => {
    let now = 0
    const matcher = createShortcutMatcher(true, () => now)
    const specs = ['P then S', 'S', 'N then C']
    expect(matcher.match(key({ key: 'p', code: 'KeyP' }), specs)).toBe('pending')
    expect(matcher.match(key({ key: 's', code: 'KeyS' }), specs)).toBe('P then S')
    expect(matcher.match(key({ key: 's', code: 'KeyS' }), specs)).toBe('S')
    expect(matcher.match(key({ key: 'n', code: 'KeyN' }), specs)).toBe('pending')
    now = 5000
    expect(matcher.match(key({ key: 'c', code: 'KeyC' }), specs)).toBeUndefined()
  })
})

function Row({ onSelect }: { onSelect: () => void }) {
  useLinearRowShortcuts(['⌥ F', 'S'])
  return <LinearContextMenuRoot>
    <LinearContextMenuTrigger asChild><a data-linear-menu-row="" href="/x" role="row">Row</a></LinearContextMenuTrigger>
    <LinearContextMenuPortal>
      <LinearMenuContent label="Row actions">
        <LinearSubmenu icon={<svg/>} label="Status" shortcut="S"><LinearMenuItem icon={<svg/>} label="Done" onSelect={vi.fn()}/></LinearSubmenu>
        <LinearMenuItem icon={<svg/>} label="Favorite" shortcut="⌥ F" onSelect={onSelect}/>
      </LinearMenuContent>
    </LinearContextMenuPortal>
  </LinearContextMenuRoot>
}

describe('Linear row context menu', () => {
  it('renders hints with the menu font, "then" between steps and no keycap borders', () => {
    render(<I18nProvider><LinearMenuShortcut value="N then C"/></I18nProvider>)
    const hint = document.querySelector('.linear-menu__shortcut')!
    expect(hint).toHaveAttribute('aria-hidden', 'true')
    expect([...hint.querySelectorAll('kbd')].map(item => item.textContent)).toEqual(['N', 'C'])
    expect(hint.textContent).toBe('NthenC')
  })

  it('runs a hint pressed over a focused row: opens the menu and selects the item', async () => {
    const onSelect = vi.fn()
    render(<I18nProvider><Row onSelect={onSelect}/></I18nProvider>)
    const row = screen.getByRole('row', { name: 'Row' })
    row.focus()
    fireEvent.keyDown(row, { key: 'ƒ', code: 'KeyF', altKey: true })
    await waitFor(() => expect(onSelect).toHaveBeenCalledTimes(1))
  })

  it('opens the hinted submenu when its key is pressed over the row', async () => {
    render(<I18nProvider><Row onSelect={vi.fn()}/></I18nProvider>)
    const row = screen.getByRole('row', { name: 'Row' })
    row.focus()
    fireEvent.keyDown(row, { key: 's', code: 'KeyS' })
    expect(await screen.findByRole('menu', { name: 'Status' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /Status/ })).toHaveAttribute('data-state', 'open')
  })
})

function Reminders({ now }: { now: Date }) {
  return <LinearContextMenuRoot>
    <LinearContextMenuTrigger asChild><a href="/x" role="row">Row</a></LinearContextMenuTrigger>
    <LinearContextMenuPortal><LinearMenuContent label="Row actions"><LinearReminderOptions now={now} onChoose={vi.fn()} onCustom={vi.fn()}/></LinearMenuContent></LinearContextMenuPortal>
  </LinearContextMenuRoot>
}

describe('Linear reminder presets', () => {
  const labels = () => screen.getAllByRole('menuitem').map(item => item.getAttribute('data-menu-item'))
  it('adds "In 3 hours" and "This evening" during the working day, with the alarm glyph on every row', () => {
    render(<I18nProvider><Reminders now={new Date(2026, 9, 7, 8, 20)}/></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Row' }))
    expect(labels()).toEqual(['An hour from now', 'In 3 hours', 'This evening', 'Tomorrow', 'Next week', 'A month from now', 'Custom…'])
    expect(document.querySelectorAll('.linear-menu__row .linear-menu__icon svg').length).toBe(7)
  })
  it('keeps the early-morning list to Linear\'s four presets', () => {
    render(<I18nProvider><Reminders now={new Date(2026, 9, 7, 7, 38)}/></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Row' }))
    expect(labels()).toEqual(['An hour from now', 'Tomorrow', 'Next week', 'A month from now', 'Custom…'])
  })
})
