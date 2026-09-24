import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import type { CodeReview } from '@/types/flow'
import { DeployPreviews } from './detail-pane'

const review = { id: 'r1', number: 7, repositoryOwner: 'acme', repositoryName: 'store', previews: [
  { id: 'p1', provider: 'github', environment: 'Preview', url: 'https://store-git-fix.vercel.app', logUrl: 'https://vercel.com/logs', state: 'ready', commitSha: 'abcdef123', createdAt: '', updatedAt: '' },
  { id: 'p2', provider: 'netlify', environment: 'Storybook', url: '', logUrl: 'https://app.netlify.com/logs', state: 'building', createdAt: '', updatedAt: '' },
  { id: 'p3', provider: 'github', environment: 'Old', url: 'https://old.example.com', state: 'inactive', createdAt: '', updatedAt: '' },
] } as unknown as CodeReview

describe('DeployPreviews', () => {
  it('links ready previews to the deployment and in-progress ones to their logs, hiding inactive', () => {
    render(<I18nProvider><TooltipProvider><DeployPreviews reviews={[review]}/></TooltipProvider></I18nProvider>)
    expect(screen.getByRole('link', { name: /Preview.*Ready/ })).toHaveAttribute('href', 'https://store-git-fix.vercel.app')
    expect(screen.getByRole('link', { name: /Storybook.*Building/ })).toHaveAttribute('href', 'https://app.netlify.com/logs')
    expect(screen.queryByText('Old')).not.toBeInTheDocument()
  })
})
