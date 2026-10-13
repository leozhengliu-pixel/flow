// Random bytes and SHA-256 for PKCE. Uses Web Crypto where it is global
// (browsers, Deno, Bun, Node 19+) and falls back to node:crypto on Node 18.

interface WebCryptoLike {
  getRandomValues<T extends ArrayBufferView>(array: T): T
  subtle: { digest(algorithm: string, data: BufferSource): Promise<ArrayBuffer> }
}

async function webCrypto(): Promise<WebCryptoLike> {
  const global = (globalThis as { crypto?: WebCryptoLike }).crypto
  if (global?.subtle) return global
  const nodeCrypto = (await import('node:crypto')) as unknown as { webcrypto: WebCryptoLike }
  return nodeCrypto.webcrypto
}

export function base64Url(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export async function randomBase64Url(byteLength: number): Promise<string> {
  const crypto = await webCrypto()
  return base64Url(crypto.getRandomValues(new Uint8Array(byteLength)))
}

export async function sha256Base64Url(value: string): Promise<string> {
  const crypto = await webCrypto()
  return base64Url(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value))))
}
