import { toast } from '@/lib/toast'
import type { LightboxItem } from './lightbox-bridge'

export function downloadImage(item: LightboxItem) {
  const link = document.createElement('a')
  link.href = item.src
  link.download = item.alt || 'image'
  link.rel = 'noopener'
  link.click()
}

export async function copyText(text: string) {
  try { await navigator.clipboard.writeText(text); toast.success('Copied to clipboard') }
  catch { toast.error('Could not copy') }
}

/** Copies the picture itself where the browser allows it, and its link otherwise. */
export async function copyImage(item: LightboxItem) {
  try {
    const blob = await (await fetch(item.src)).blob()
    await navigator.clipboard.write([new ClipboardItem({ [blob.type || 'image/png']: blob })])
    toast.success('Copied to clipboard')
  } catch {
    await copyText(item.src)
  }
}
