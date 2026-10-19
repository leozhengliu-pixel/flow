// Uploads screenshots and videos to Flow:
//   POST {FLOW_RUNTIME_URL}/internal/coding-runtime/artifacts
//   Authorization: Bearer <sandbox token>
//   multipart/form-data: file, kind, label, group?, turnId?, clientId
//   → 201/200 {"id": string, "url": string}
//
// 4xx responses are final. 5xx responses and network errors are retried with jittered exponential
// backoff, reusing the same clientId so the server can de-duplicate a retry of a request that did
// succeed server-side.
import { randomUUID } from 'node:crypto';

import { artifactMaxBytes, currentTurnId, readToken, runtimeUrl } from './config.js';
import { ToolError, errorMessage, formatBytes, sleep as defaultSleep, truncate } from './util.js';

export type ArtifactKind = 'screenshot' | 'video';
export type ArtifactMime = 'image/png' | 'image/jpeg' | 'video/webm' | 'video/mp4';

export interface UploadRequest {
  bytes: Uint8Array;
  filename: string;
  contentType: ArtifactMime;
  kind: ArtifactKind;
  label: string;
  group?: string;
}

export interface UploadedArtifact {
  id: string;
  url: string;
}

export interface UploaderOptions {
  env: NodeJS.ProcessEnv;
  fetch?: typeof fetch;
  sleep?: (ms: number) => Promise<void>;
  random?: () => number;
  attempts?: number;
  baseDelayMs?: number;
  maxDelayMs?: number;
  requestTimeoutMs?: number;
}

export const ARTIFACTS_PATH = '/internal/coding-runtime/artifacts';

class RetryableError extends Error {}

export class ArtifactUploader {
  private readonly env: NodeJS.ProcessEnv;
  private readonly fetchImpl: typeof fetch;
  private readonly sleep: (ms: number) => Promise<void>;
  private readonly random: () => number;
  private readonly attempts: number;
  private readonly baseDelayMs: number;
  private readonly maxDelayMs: number;
  private readonly requestTimeoutMs: number;

  constructor(opts: UploaderOptions) {
    this.env = opts.env;
    this.fetchImpl = opts.fetch ?? globalThis.fetch.bind(globalThis);
    this.sleep = opts.sleep ?? defaultSleep;
    this.random = opts.random ?? Math.random;
    this.attempts = opts.attempts ?? 4;
    this.baseDelayMs = opts.baseDelayMs ?? 500;
    this.maxDelayMs = opts.maxDelayMs ?? 8000;
    this.requestTimeoutMs = opts.requestTimeoutMs ?? 60_000;
  }

  get maxBytes(): number {
    return artifactMaxBytes(this.env);
  }

  async upload(req: UploadRequest): Promise<UploadedArtifact> {
    const max = this.maxBytes;
    if (req.bytes.byteLength > max) throw new ToolError(tooLargeMessage(req.kind, req.bytes.byteLength, max));

    const endpoint = `${runtimeUrl(this.env)}${ARTIFACTS_PATH}`;
    const turnId = await currentTurnId(this.env);
    const clientId = randomUUID();

    let lastError = '';
    for (let attempt = 0; attempt < this.attempts; attempt++) {
      if (attempt > 0) await this.sleep(this.backoff(attempt - 1));
      try {
        // Re-read the token per attempt in case the runner rotated it.
        const token = await readToken(this.env);
        return await this.once(endpoint, token, req, clientId, turnId, max);
      } catch (err) {
        if (!(err instanceof RetryableError)) throw err;
        lastError = err.message;
      }
    }
    throw new ToolError(`Uploading the ${req.kind} to Flow failed after ${this.attempts} attempts: ${lastError}`);
  }

  private backoff(n: number): number {
    const exp = Math.min(this.maxDelayMs, this.baseDelayMs * 2 ** n);
    // Jitter between 50% and 100% of the exponential delay.
    return Math.round(exp * (0.5 + this.random() * 0.5));
  }

  private async once(
    endpoint: string,
    token: string,
    req: UploadRequest,
    clientId: string,
    turnId: string | undefined,
    max: number,
  ): Promise<UploadedArtifact> {
    const form = new FormData();
    // Copy into a plain ArrayBuffer-backed view (BlobPart excludes SharedArrayBuffer views).
    form.append('file', new Blob([new Uint8Array(req.bytes)], { type: req.contentType }), req.filename);
    form.append('kind', req.kind);
    form.append('label', req.label);
    if (req.group) form.append('group', req.group);
    if (turnId) form.append('turnId', turnId);
    form.append('clientId', clientId);

    let res: Response;
    try {
      res = await this.fetchImpl(endpoint, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}`, Accept: 'application/json' },
        body: form,
        signal: AbortSignal.timeout(this.requestTimeoutMs),
      });
    } catch (err) {
      const name = err instanceof Error ? err.name : '';
      const reason =
        name === 'TimeoutError' || name === 'AbortError'
          ? `request timed out after ${Math.round(this.requestTimeoutMs / 1000)}s`
          : `network error (${errorMessage(err)})`;
      throw new RetryableError(reason);
    }

    const body = await readBody(res, this.requestTimeoutMs);
    if (res.status >= 500) throw new RetryableError(`HTTP ${res.status}${body.message ? `: ${body.message}` : ''}`);
    if (res.status === 200 || res.status === 201) {
      const json = body.json as { id?: unknown; url?: unknown } | undefined;
      if (!json || typeof json.id !== 'string' || typeof json.url !== 'string') {
        throw new ToolError('Flow accepted the upload but returned an unexpected response (missing id/url).');
      }
      return { id: json.id, url: json.url };
    }
    throw new ToolError(clientErrorMessage(req.kind, res.status, body.message, max));
  }
}

async function readBody(res: Response, timeoutMs: number): Promise<{ json?: unknown; message?: string | undefined }> {
  let raw = '';
  try {
    let timer: NodeJS.Timeout | undefined;
    raw = await Promise.race([
      res.text(),
      new Promise<string>((resolve) => {
        timer = setTimeout(() => resolve(''), timeoutMs);
      }),
    ]).finally(() => timer && clearTimeout(timer));
  } catch {
    return {};
  }
  if (!raw) return {};
  try {
    const json = JSON.parse(raw) as unknown;
    let message: string | undefined;
    if (json && typeof json === 'object') {
      const obj = json as Record<string, unknown>;
      const candidate = obj.error ?? obj.message;
      if (typeof candidate === 'string') message = candidate;
      else if (candidate && typeof candidate === 'object' && typeof (candidate as Record<string, unknown>).message === 'string') {
        message = (candidate as Record<string, string>).message;
      }
    }
    return { json, message: message ? truncate(message, 300) : undefined };
  } catch {
    return { message: truncate(raw.trim(), 300) || undefined };
  }
}

export function tooLargeMessage(kind: ArtifactKind, size: number, max: number): string {
  const limit = formatBytes(max);
  if (kind === 'video') {
    return `Video is larger than the ${limit} limit (${formatBytes(size)}); record a shorter flow or use a smaller viewport.`;
  }
  return `Screenshot is larger than the ${limit} limit (${formatBytes(size)}); take a smaller viewport or a JPEG.`;
}

function clientErrorMessage(kind: ArtifactKind, status: number, serverMessage: string | undefined, max: number): string {
  const detail = serverMessage ? ` Server said: ${serverMessage}` : '';
  switch (status) {
    case 413:
      return `${kind === 'video' ? 'Video' : 'Screenshot'} is larger than the ${formatBytes(max)} limit accepted by Flow (HTTP 413); ${
        kind === 'video' ? 'record a shorter flow or use a smaller viewport' : 'take a smaller viewport or a JPEG'
      }.${detail}`;
    case 429:
      return `Artifact limit reached for this turn (at most 20 screenshots/videos per turn); attach only the most important captures.${detail}`;
    case 401:
    case 403:
      return `Flow rejected the artifact upload (HTTP ${status}): the sandbox token is invalid or expired.${detail}`;
    case 415:
      return `Flow rejected the file type (HTTP 415); only PNG, JPEG, WebM and MP4 are accepted.${detail}`;
    default:
      return `Flow rejected the artifact upload (HTTP ${status}).${detail}`;
  }
}
