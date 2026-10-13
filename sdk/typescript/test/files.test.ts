import { describe, expect, it } from 'vitest'
import { FlowClient, InvalidInputFlowError, RatelimitedFlowError } from '../src/index.js'
import { API_URL, envelope, json, mockFetch, recordingSleep } from './helpers.js'

const uploadUrl = `${API_URL}/api/files/uploads/upload_abc123`
const prepared = {
  id: 'file_1',
  uploadUrl,
  assetUrl: `${API_URL}/uploads/asset_1.png`,
  filename: 'shot.png',
  contentType: 'image/png',
  size: 5,
  headers: [{ key: 'Content-Type', value: 'image/png' }, { key: 'X-Flow-Upload', value: 'yes' }],
  expiresAt: '2026-10-12T17:15:00Z',
}

function setup(upload: (request: ReturnType<typeof mockFetch>['calls'][number]) => Response = () => new Response(null, { status: 200 })) {
  const mock = mockFetch(request => (request.path === '/api/files' ? json(prepared) : request.path.startsWith('/api/files/sign') ? json({ files: [] }) : upload(request)))
  return { ...mock, flow: new FlowClient({ apiKey: 'flow_api_secret', apiUrl: API_URL, fetch: mock.fetch, headers: { 'X-Custom': 'yes' } }) }
}

describe('uploadFile', () => {
  it('prepares, PUTs the bytes without credentials, and returns the asset URL', async () => {
    const { flow, calls } = setup()
    const bytes = new Uint8Array([1, 2, 3, 4, 5])
    const upload = await flow.uploadFile(bytes, { filename: 'shot.png', contentType: 'image/png' })

    expect(upload.assetUrl).toBe(`${API_URL}/uploads/asset_1.png`)
    expect(calls).toHaveLength(2)

    const [prepare, put] = calls
    expect(prepare.method).toBe('POST')
    expect(prepare.path).toBe('/api/files')
    expect(prepare.headers.get('authorization')).toBe('Bearer flow_api_secret')
    expect(prepare.json).toEqual({ filename: 'shot.png', contentType: 'image/png', size: 5 })

    expect(put.method).toBe('PUT')
    expect(put.url).toBe(uploadUrl)
    expect(put.headers.get('authorization')).toBeNull()
    expect(put.headers.get('x-custom')).toBeNull()
    expect(put.headers.get('content-type')).toBe('image/png')
    expect(put.headers.get('x-flow-upload')).toBe('yes')
    expect([...put.bytes!]).toEqual([1, 2, 3, 4, 5])
  })

  it('derives name, type and size from a File', async () => {
    const { flow, calls } = setup()
    const file = new File(['hello world'], 'notes.txt', { type: 'text/plain' })
    await flow.uploadFile(file)
    expect(calls[0].json).toEqual({ filename: 'notes.txt', contentType: 'text/plain', size: 11 })
  })

  it('measures strings in UTF-8 bytes', async () => {
    const { flow, calls } = setup()
    await flow.uploadFile('héllo ✓', { filename: 'a.txt', contentType: 'text/plain' })
    expect(calls[0].json.size).toBe(Buffer.byteLength('héllo ✓'))
    expect(calls[1].bytes!.byteLength).toBe(Buffer.byteLength('héllo ✓'))
  })

  it('accepts ArrayBuffer and Buffer bodies and defaults the content type', async () => {
    const { flow, calls } = setup()
    await flow.uploadFile(new ArrayBuffer(8))
    await flow.uploadFile(Buffer.from('abc'), { filename: 'abc' })
    expect(calls[0].json).toEqual({ filename: 'upload', contentType: 'application/octet-stream', size: 8 })
    expect(calls[2].json).toEqual({ filename: 'abc', contentType: 'application/octet-stream', size: 3 })
  })

  it('rejects unsupported bodies', async () => {
    const { flow } = setup()
    await expect(flow.uploadFile({} as never)).rejects.toThrow(TypeError)
  })

  it('maps upload failures to errors (size mismatch, rate limit)', async () => {
    const mismatch = setup(() => envelope('INVALID_INPUT', 'size mismatch', 400))
    await expect(mismatch.flow.uploadFile(new Uint8Array(5), { filename: 'a', contentType: 'x/y' })).rejects.toBeInstanceOf(InvalidInputFlowError)

    const limited = setup(() => envelope('RATELIMITED', 'upload budget', 429, {}, { 'Retry-After': '60', 'X-RateLimit-Endpoint-Name': 'fileUpload' }))
    const error = await limited.flow.uploadFile(new Uint8Array(5), { filename: 'a', contentType: 'x/y' }).catch(e => e)
    expect(error).toBeInstanceOf(RatelimitedFlowError)
    expect(error.rateLimit.endpointName).toBe('fileUpload')
  })

  it('retries a failed PUT when retries are enabled', async () => {
    const { sleep } = recordingSleep()
    let puts = 0
    const mock = mockFetch(request => {
      if (request.path === '/api/files') return json(prepared)
      return ++puts === 1 ? envelope('UNAVAILABLE', 'down', 503) : new Response(null, { status: 200 })
    })
    const flow = new FlowClient({ apiKey: 'k', apiUrl: API_URL, fetch: mock.fetch, retry: { sleep, jitter: false } })
    await flow.uploadFile(new Uint8Array(5), { filename: 'a', contentType: 'image/png' })
    expect(puts).toBe(2)
  })

  it('prepares an upload without sending bytes', async () => {
    const { flow, calls } = setup()
    expect((await flow.prepareFileUpload({ filename: 'a.png', contentType: 'image/png', size: 5 })).id).toBe('file_1')
    expect(calls).toHaveLength(1)
  })
})

describe('signed URLs and downloads', () => {
  it('signs file URLs, chunking requests of more than 100', async () => {
    const mock = mockFetch(request => json({ files: request.query.getAll('url').map(url => ({ url, signedUrl: `${url}?signature=x`, expiresAt: '2026-10-12T18:00:00Z' })) }))
    const flow = new FlowClient({ apiKey: 'k', apiUrl: API_URL, fetch: mock.fetch })
    const urls = Array.from({ length: 150 }, (_, i) => `${API_URL}/uploads/asset_${i}.png`)
    const signed = await flow.signFileUrls(urls, 600)
    expect(signed).toHaveLength(150)
    expect(mock.calls).toHaveLength(2)
    expect(mock.calls[0].query.getAll('url')).toHaveLength(100)
    expect(mock.calls[0].query.get('expiresIn')).toBe('600')
    expect(signed[0].signedUrl).toBe(`${urls[0]}?signature=x`)
  })

  it('signs a single URL', async () => {
    const mock = mockFetch(() => json({ files: [{ url: 'u', signedUrl: 's', expiresAt: 'e' }] }))
    const flow = new FlowClient({ apiKey: 'k', apiUrl: API_URL, fetch: mock.fetch })
    expect(await flow.signFileUrls(`${API_URL}/uploads/a.png`)).toHaveLength(1)
    expect(mock.calls[0].query.get('expiresIn')).toBeNull()
  })

  it('downloads private files with credentials', async () => {
    const mock = mockFetch(() => new Response('PNG', { status: 200, headers: { 'Content-Type': 'image/png' } }))
    const flow = new FlowClient({ apiKey: 'flow_api_secret', apiUrl: API_URL, fetch: mock.fetch })
    const response = await flow.downloadFile('/uploads/asset_1.png')
    expect(await response.text()).toBe('PNG')
    expect(mock.calls[0].url).toBe(`${API_URL}/uploads/asset_1.png`)
    expect(mock.calls[0].headers.get('authorization')).toBe('Bearer flow_api_secret')
  })

  it('maps download errors', async () => {
    const mock = mockFetch(() => envelope('NOT_FOUND', 'file not found', 404))
    const flow = new FlowClient({ apiKey: 'k', apiUrl: API_URL, fetch: mock.fetch })
    await expect(flow.downloadFile(`${API_URL}/uploads/missing.png`)).rejects.toMatchObject({ type: 'NOT_FOUND' })
  })
})
