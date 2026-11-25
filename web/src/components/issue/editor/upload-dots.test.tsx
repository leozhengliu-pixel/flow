import { act, render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { reportUploadProgress, clearUploadProgress, markUploadStarted } from '@/lib/upload-progress'
import { DescriptionUploading, UploadDots } from './description-media'

const opacities = (svg: Element) => [...svg.querySelectorAll('circle')].map(circle => circle.getAttribute('opacity'))

describe('upload progress dots', () => {
  it('fills row by row: lit dots below the newest, the newest blinking, the rest dim', () => {
    const { container } = render(<UploadDots fraction={0.4}/>)
    const svg = container.querySelector('svg')!
    expect(svg.getAttribute('aria-label')).toBe('Progress 40%')
    const dots = opacities(svg)
    expect(dots).toHaveLength(25)
    expect(dots.slice(0, 10).every(value => value === '1')).toBe(true)
    expect(dots.slice(10).every(value => value === '0.3')).toBe(true)
    expect([...svg.querySelectorAll('circle')].findIndex(circle => circle.classList.contains('description-upload-dot-pulse'))).toBe(9)
  })

  it('shows every dot dim and none blinking before the first 4%', () => {
    const { container } = render(<UploadDots fraction={0.03}/>)
    expect(opacities(container.querySelector('svg')!).every(value => value === '0.3')).toBe(true)
    expect(container.querySelector('.description-upload-dot-pulse')).toBeNull()
  })

  it('follows the progress reported for its upload', () => {
    markUploadStarted('blob:x')
    const { container } = render(<DescriptionUploading src="blob:x"/>)
    expect(container.querySelector('svg')!.getAttribute('aria-label')).toBe('Progress 0%')
    act(() => reportUploadProgress('blob:x', 0.12))
    expect(container.querySelector('svg')!.getAttribute('aria-label')).toBe('Progress 12%')
    act(() => clearUploadProgress('blob:x'))
  })

  it('offers to remove a placeholder that no upload in this page feeds', () => {
    const remove = vi.fn()
    const { container } = render(<DescriptionUploading onRemove={remove} src="blob:left-over"/>)
    expect(container.querySelector('svg')).toBeNull()
    expect(container.textContent).toContain("Upload didn't complete")
    container.querySelector('button')!.click()
    expect(remove).toHaveBeenCalledTimes(1)
  })
})
