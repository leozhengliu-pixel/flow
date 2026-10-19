import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ArtifactUploader, type UploadRequest } from './upload.js';

const TOKEN = 'fcs_secret_token_value';

let dir: string;
let env: NodeJS.ProcessEnv;

beforeEach(async () => {
  dir = await mkdtemp(path.join(tmpdir(), 'flow-upload-'));
  await writeFile(path.join(dir, 'token'), `${TOKEN}\n`);
  env = {
    FLOW_RUNTIME_URL: 'http://flow.test:8081/',
    FLOW_TOKEN_FILE: path.join(dir, 'token'),
    FLOW_TURN_ID: 'turn_123',
    HOME: dir,
  };
});

afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

function req(overrides: Partial<UploadRequest> = {}): UploadRequest {
  return {
    bytes: new Uint8Array([0x89, 0x50, 0x4e, 0x47, 1, 2, 3]),
    filename: 'after.png',
    contentType: 'image/png',
    kind: 'screenshot',
    label: 'after',
    group: 'login',
    ...overrides,
  };
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
}

type FetchMock = ReturnType<typeof vi.fn<typeof fetch>>;

function uploader(fetchMock: FetchMock, sleep = vi.fn(async (_ms: number) => undefined)) {
  return { up: new ArtifactUploader({ env, fetch: fetchMock, sleep, random: () => 1 }), sleep };
}

function formOf(call: Parameters<typeof fetch>): FormData {
  const body = call[1]?.body;
  expect(body).toBeInstanceOf(FormData);
  return body as FormData;
}

describe('ArtifactUploader', () => {
  it('posts multipart fields with the bearer token', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => json(201, { id: 'art_1', url: 'https://flow.test/a/1' }));
    const { up } = uploader(fetchMock);
    await expect(up.upload(req())).resolves.toEqual({ id: 'art_1', url: 'https://flow.test/a/1' });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const call = fetchMock.mock.calls[0]!;
    expect(call[0]).toBe('http://flow.test:8081/internal/coding-runtime/artifacts');
    expect(call[1]?.method).toBe('POST');
    expect((call[1]?.headers as Record<string, string>).Authorization).toBe(`Bearer ${TOKEN}`);
    expect(call[1]?.signal).toBeInstanceOf(AbortSignal);
    const form = formOf(call);
    expect(form.get('kind')).toBe('screenshot');
    expect(form.get('label')).toBe('after');
    expect(form.get('group')).toBe('login');
    expect(form.get('turnId')).toBe('turn_123');
    expect(form.get('clientId')).toMatch(/^[0-9a-f-]{36}$/);
    const file = form.get('file') as File;
    expect(file.name).toBe('after.png');
    expect(file.type).toBe('image/png');
    expect(new Uint8Array(await file.arrayBuffer())).toEqual(req().bytes);
  });

  it('omits optional fields and reads the turn id from $HOME/.flow/current-turn', async () => {
    delete env.FLOW_TURN_ID;
    await mkdir(path.join(dir, '.flow'));
    await writeFile(path.join(dir, '.flow', 'current-turn'), 'turn_from_file\n');
    const fetchMock = vi.fn<typeof fetch>(async () => json(200, { id: 'a', url: 'u' }));
    const { up } = uploader(fetchMock);
    const { group: _omitted, ...withoutGroup } = req();
    await up.upload(withoutGroup);
    const form = formOf(fetchMock.mock.calls[0]!);
    expect(form.has('group')).toBe(false);
    expect(form.get('turnId')).toBe('turn_from_file');
  });

  it('omits turnId when neither env nor file has one', async () => {
    delete env.FLOW_TURN_ID;
    const fetchMock = vi.fn<typeof fetch>(async () => json(201, { id: 'a', url: 'u' }));
    await uploader(fetchMock).up.upload(req());
    expect(formOf(fetchMock.mock.calls[0]!).has('turnId')).toBe(false);
  });

  it('retries 5xx with backoff and reuses the clientId', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(json(503, { error: 'busy' }))
      .mockResolvedValueOnce(json(502, {}))
      .mockResolvedValueOnce(json(201, { id: 'art_2', url: 'u' }));
    const { up, sleep } = uploader(fetchMock);
    await expect(up.upload(req())).resolves.toEqual({ id: 'art_2', url: 'u' });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const ids = fetchMock.mock.calls.map((c) => formOf(c).get('clientId'));
    expect(new Set(ids).size).toBe(1);
    expect(sleep.mock.calls.map((c) => c[0])).toEqual([500, 1000]);
  });

  it('retries network errors and gives up after 4 attempts with a clear message', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => {
      throw new TypeError('fetch failed');
    });
    const { up, sleep } = uploader(fetchMock);
    const err = await up.upload(req()).catch((e: Error) => e);
    expect(err).toBeInstanceOf(Error);
    expect((err as Error).message).toMatch(/failed after 4 attempts: network error \(fetch failed\)/);
    expect((err as Error).message).not.toContain(TOKEN);
    expect(fetchMock).toHaveBeenCalledTimes(4);
    expect(sleep.mock.calls.map((c) => c[0])).toEqual([500, 1000, 2000]);
  });

  it('caps the backoff delay', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => json(500, {}));
    const sleep = vi.fn(async (_ms: number) => undefined);
    const up = new ArtifactUploader({ env, fetch: fetchMock, sleep, random: () => 1, attempts: 7 });
    await expect(up.upload(req())).rejects.toThrow(/HTTP 500/);
    expect(sleep.mock.calls.map((c) => c[0])).toEqual([500, 1000, 2000, 4000, 8000, 8000]);
  });

  it('does not retry 413 and explains the size limit', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => json(413, { error: 'too large' }));
    const { up, sleep } = uploader(fetchMock);
    await expect(up.upload(req())).rejects.toThrow(
      /Screenshot is larger than the 10 MiB limit accepted by Flow \(HTTP 413\); take a smaller viewport or a JPEG\. Server said: too large/,
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(sleep).not.toHaveBeenCalled();
  });

  it('maps 429, 401 and other 4xx to clear messages without retrying', async () => {
    const cases: Array<[number, RegExp]> = [
      [429, /Artifact limit reached for this turn.*Server said: limit 20/],
      [401, /sandbox token is invalid or expired/],
      [415, /only PNG, JPEG, WebM and MP4/],
      [400, /Flow rejected the artifact upload \(HTTP 400\)/],
    ];
    for (const [status, re] of cases) {
      const fetchMock = vi.fn<typeof fetch>(async () => json(status, { error: 'limit 20' }));
      await expect(uploader(fetchMock).up.upload(req())).rejects.toThrow(re);
      expect(fetchMock).toHaveBeenCalledTimes(1);
    }
  });

  it('accepts plain-text error bodies', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => new Response('nope', { status: 400 }));
    await expect(uploader(fetchMock).up.upload(req())).rejects.toThrow(/Server said: nope/);
  });

  it('rejects a malformed success body', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => json(201, { ok: true }));
    await expect(uploader(fetchMock).up.upload(req())).rejects.toThrow(/unexpected response/);
  });

  it('refuses files over FLOW_CODING_ARTIFACT_MAX_BYTES before sending', async () => {
    env.FLOW_CODING_ARTIFACT_MAX_BYTES = '4';
    const fetchMock = vi.fn<typeof fetch>();
    await expect(uploader(fetchMock).up.upload(req())).rejects.toThrow(/larger than the 4 bytes limit/);
    await expect(uploader(fetchMock).up.upload(req({ kind: 'video', contentType: 'video/webm' }))).rejects.toThrow(
      /Video is larger.*shorter flow or use a smaller viewport/,
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reports missing configuration clearly', async () => {
    const fetchMock = vi.fn<typeof fetch>();
    delete env.FLOW_RUNTIME_URL;
    await expect(uploader(fetchMock).up.upload(req())).rejects.toThrow(/FLOW_RUNTIME_URL is not set/);
    env.FLOW_RUNTIME_URL = 'http://flow.test';
    env.FLOW_TOKEN_FILE = path.join(dir, 'missing');
    await expect(uploader(fetchMock).up.upload(req())).rejects.toThrow(/token file is not readable/);
    delete env.FLOW_TOKEN_FILE;
    await expect(uploader(fetchMock).up.upload(req())).rejects.toThrow(/FLOW_TOKEN_FILE is not set/);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('treats a request timeout as retryable', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (_u, init) => {
      await new Promise((resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(init.signal?.reason as Error));
      });
      return json(201, {});
    });
    const up = new ArtifactUploader({ env, fetch: fetchMock, sleep: async () => undefined, attempts: 2, requestTimeoutMs: 20 });
    await expect(up.upload(req())).rejects.toThrow(/failed after 2 attempts: request timed out/);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
