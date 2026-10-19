import type { ToolContent, ToolResult } from './types.js';

/** Error whose message is safe and useful to show to the agent verbatim. */
export class ToolError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ToolError';
  }
}

export class TimeoutError extends ToolError {
  constructor(label: string, ms: number) {
    super(`${label} timed out after ${formatSeconds(ms)}`);
    this.name = 'TimeoutError';
  }
}

export function formatSeconds(ms: number): string {
  const s = ms / 1000;
  return Number.isInteger(s) ? `${s}s` : `${s.toFixed(1)}s`;
}

/**
 * Backstop timeout for any awaited browser or network operation. Playwright calls also get their
 * own `timeout` option; this guarantees a tool call can never hang if the driver itself wedges.
 */
export async function withTimeout<T>(promise: Promise<T>, ms: number, label: string): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new TimeoutError(label, ms)), ms);
  });
  try {
    return await Promise.race([promise, timeout]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

export const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

/** Fixed-capacity FIFO keeping the most recent items. */
export class RingBuffer<T> {
  private items: T[] = [];
  private total = 0;

  constructor(private readonly capacity: number) {}

  push(item: T): void {
    this.items.push(item);
    this.total += 1;
    if (this.items.length > this.capacity) this.items.splice(0, this.items.length - this.capacity);
  }

  /** The last `n` items (all retained items when omitted). */
  tail(n?: number): T[] {
    return n === undefined ? [...this.items] : this.items.slice(Math.max(0, this.items.length - n));
  }

  /** Total number of items ever pushed; used as a cursor for "items since X". */
  get count(): number {
    return this.total;
  }

  /** Items pushed after the cursor value returned by `count` earlier (bounded by capacity). */
  since(cursor: number): T[] {
    const newer = this.total - cursor;
    if (newer <= 0) return [];
    return this.items.slice(Math.max(0, this.items.length - newer));
  }

  clear(): void {
    this.items = [];
  }
}

export function truncate(text: string, max: number): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max)}\n… [truncated ${text.length - max} characters]`;
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * Playwright errors carry a long "Call log:" section and ANSI colour codes. Keep the first useful
 * part so the agent gets a readable message.
 */
export function cleanDriverError(err: unknown): string {
  let msg = stripAnsi(errorMessage(err));
  const callLog = msg.indexOf('\nCall log:');
  if (callLog > 0) {
    const log = msg
      .slice(callLog + '\nCall log:'.length)
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
      .slice(0, 6)
      .join('\n  ');
    msg = `${msg.slice(0, callLog).trim()}${log ? `\n  ${log}` : ''}`;
  }
  return truncate(msg.trim(), 1500);
}

export function stripAnsi(text: string): string {
  // eslint-disable-next-line no-control-regex
  return text.replace(/\u001b\[[0-9;?]*[ -/]*[@-~]/g, '');
}

export function text(t: string): ToolContent {
  return { type: 'text', text: t };
}

export function ok(...blocks: Array<string | ToolContent>): ToolResult {
  return { content: blocks.map((b) => (typeof b === 'string' ? text(b) : b)) };
}

export function fail(message: string, ...extra: ToolContent[]): ToolResult {
  return { content: [text(message), ...extra], isError: true };
}

export function formatBytes(n: number): string {
  if (n >= 1024 * 1024) {
    const mib = n / (1024 * 1024);
    return `${Number.isInteger(mib) ? mib : mib.toFixed(1)} MiB`;
  }
  if (n >= 1024) return `${Math.round(n / 1024)} KiB`;
  return `${n} bytes`;
}

/** Filesystem-safe slug for file names derived from labels. */
export function slugify(label: string): string {
  const slug = label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40);
  return slug || 'capture';
}

/** Serializes async operations that mutate shared state. */
export class Mutex {
  private tailPromise: Promise<unknown> = Promise.resolve();

  run<T>(fn: () => Promise<T>): Promise<T> {
    const result = this.tailPromise.then(fn, fn);
    this.tailPromise = result.catch(() => undefined);
    return result;
  }
}
