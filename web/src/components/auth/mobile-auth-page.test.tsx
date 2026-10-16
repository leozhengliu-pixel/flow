import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { MobileAuthPage } from './mobile-auth-page'

describe('mobile authentication handoff', () => {
  it.each(['javascript:alert(1)', 'data:text/html,<script>alert(1)</script>', 'https://attacker.example/auth', '//attacker.example'])('rejects %s', target => {
    render(<MemoryRouter initialEntries={[`/auth/mobile?redirectUri=${encodeURIComponent(target)}`]}><MobileAuthPage /></MemoryRouter>)
    expect(screen.queryByRole('link', { name: 'Open Flow' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Continue in browser' })).toBeInTheDocument()
  })

  it('allows the Flow application scheme', () => {
    render(<MemoryRouter initialEntries={['/auth/mobile?redirectUri=flow%3A%2F%2Fauth%3Fcode%3D123']}><MobileAuthPage /></MemoryRouter>)
    expect(screen.getByRole('link', { name: 'Open Flow' })).toHaveAttribute('href', 'flow://auth?code=123')
  })
})
