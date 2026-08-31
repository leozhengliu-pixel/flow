import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { SettingsPageTitle, SettingsRow, SettingsSection, SettingsSelect, SettingsToggle } from './settings-primitives'

function SettingsHarness({ onSelect }: { onSelect: (value: string) => void }) {
  const [enabled, setEnabled] = useState(false)
  const [value, setValue] = useState('Daily')
  return <>
    <SettingsPageTitle action={<button>Save</button>} description="Workspace preferences">Settings</SettingsPageTitle>
    <SettingsSection description="Delivery controls" title="Updates">
      <SettingsRow description="Send reminders" title="Notifications">
        <SettingsToggle checked={enabled} label="Enable reminders" onChange={setEnabled}/>
      </SettingsRow>
      <SettingsRow control={false} danger title="Danger zone"><span>Protected</span></SettingsRow>
      <SettingsSelect label="Cadence" onChange={next => { setValue(next); onSelect(next) }} options={['Daily', 'Weekly']} value={value}/>
    </SettingsSection>
  </>
}

describe('settings primitives', () => {
  it('renders semantic sections and handles toggles and select menus', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const { container } = render(<SettingsHarness onSelect={onSelect}/>)
    expect(screen.getByRole('heading', { name: 'Settings' })).toBeVisible()
    expect(screen.getByText('Workspace preferences')).toBeVisible()
    await user.click(screen.getByRole('checkbox', { name: 'Enable reminders' }))
    expect(screen.getByRole('checkbox', { name: 'Enable reminders' })).toBeChecked()
    screen.getByRole('combobox', { name: 'Cadence' }).focus()
    await user.keyboard('{Enter}')
    await user.click(await screen.findByRole('option', { name: 'Weekly' }))
    expect(onSelect).toHaveBeenCalledWith('Weekly')
    expect(container.querySelector('.settings-row.danger')).not.toBeNull()
  })
})
