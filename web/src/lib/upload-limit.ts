import type { BootstrapData } from '@/types/flow'

/** The server's default upload limit (server.storage.uploadMaxBytes in the config file), used until a bootstrap says otherwise. */
const DEFAULT_UPLOAD_MAX_BYTES = 100 * 1024 * 1024

let rememberedBytes: number | undefined

/** Keeps the upload limit of the last loaded bootstrap, for upload checks made without workspace data at hand. */
export function rememberUploadLimit<T extends Pick<BootstrapData, 'uploadMaxBytes'>>(data: T): T {
  if (typeof data.uploadMaxBytes === 'number' && data.uploadMaxBytes > 0) rememberedBytes = data.uploadMaxBytes
  return data
}

/** The largest file the server stores from one upload (server.storage.uploadMaxBytes in the config file). */
export function uploadLimitBytes(data?: Pick<BootstrapData, 'uploadMaxBytes'> | null): number {
  const bytes = data?.uploadMaxBytes
  if (typeof bytes === 'number' && bytes > 0) return bytes
  return rememberedBytes ?? DEFAULT_UPLOAD_MAX_BYTES
}

/** A byte limit as the app writes it, in MB of 1024 × 1024 bytes like the server's messages: "100 MB", "1.5 MB". */
export function formatUploadLimit(bytes: number): string {
  const megabytes = bytes / (1024 * 1024)
  return `${Number.isInteger(megabytes) ? megabytes : Number(megabytes.toFixed(1))} MB`
}

/** Fills `{size}` in an already translated text with a formatted byte limit. */
export function withUploadLimit(text: string, bytes: number): string {
  return text.replaceAll('{size}', () => formatUploadLimit(bytes))
}

/** Forgets the remembered limit (tests). */
export function resetRememberedUploadLimit() {
  rememberedBytes = undefined
}
