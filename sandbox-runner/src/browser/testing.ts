// Test doubles for the Playwright-shaped driver interfaces. Used only by *.test.ts files; nothing in
// production code imports this module.
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

import type {
  BrowserLauncher,
  BrowserLike,
  ContextLike,
  ContextOptions,
  LaunchOptions,
  LocatorLike,
  PageLike,
  ResponseLike,
  ScreenshotOptions,
  VideoLike,
  Viewport,
} from './types.js';

export const PNG_MAGIC = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
export const JPEG_MAGIC = [0xff, 0xd8, 0xff, 0xe0];
export const WEBM_MAGIC = [0x1a, 0x45, 0xdf, 0xa3];

export function bytesWithMagic(magic: number[], size: number): Uint8Array {
  const out = new Uint8Array(Math.max(size, magic.length));
  out.set(magic, 0);
  return out;
}

export type Call = [string, ...unknown[]];

export interface FakeBehaviour {
  /** Status returned by goto. */
  status: number;
  title: string;
  /** Size of PNG / JPEG screenshots in bytes. */
  pngSize: number;
  jpegSize: number;
  /** Size of the video written when a recording context closes. */
  videoSize: number;
  /** Visible text returned by innerText. */
  innerText: string;
  /** Method names that should reject (e.g. "click", "waitFor", "goto"). */
  failOn: Set<string>;
  /** Console errors emitted on every goto. */
  consoleErrorsOnGoto: string[];
  /** Failed responses emitted on every goto. */
  failedResponsesOnGoto: Array<{ url: string; status: number }>;
  /** Make launch() reject with this message. */
  launchError?: string;
}

export class FakeLocator implements LocatorLike {
  constructor(
    private readonly page: FakePage,
    readonly description: string,
  ) {}

  first(): LocatorLike {
    return this;
  }

  private act(method: string, ...args: unknown[]): Promise<void> {
    this.page.calls.push([method, this.description, ...args]);
    if (this.page.behaviour.failOn.has(method)) {
      return Promise.reject(new Error(`locator.${method}: Timeout 15000ms exceeded.\nCall log:\n  - waiting for ${this.description}`));
    }
    return Promise.resolve();
  }

  click(options: { timeout: number }): Promise<void> {
    return this.act('click', options);
  }
  fill(value: string, options: { timeout: number }): Promise<void> {
    return this.act('fill', value, options);
  }
  pressSequentially(text: string, options: { timeout: number }): Promise<void> {
    return this.act('pressSequentially', text, options);
  }
  press(key: string, options: { timeout: number }): Promise<void> {
    return this.act('press', key, options);
  }
  waitFor(options: { state: 'visible' | 'attached'; timeout: number }): Promise<void> {
    return this.act('waitFor', options);
  }
  async innerText(options: { timeout: number }): Promise<string> {
    await this.act('innerText', options);
    return this.page.behaviour.innerText;
  }
  async screenshot(options: Omit<ScreenshotOptions, 'fullPage'>): Promise<Uint8Array> {
    await this.act('screenshot', options);
    return this.page.shot(options.type);
  }
}

export class FakePage implements PageLike {
  readonly calls: Call[] = [];
  private currentUrl = 'about:blank';
  private readonly listeners = new Map<string, Array<(arg: unknown) => void>>();
  viewport: Viewport;
  closed = false;

  constructor(
    readonly behaviour: FakeBehaviour,
    readonly context: FakeContext,
  ) {
    this.viewport = context.options.viewport;
  }

  keyboard = {
    press: async (key: string): Promise<void> => {
      this.calls.push(['keyboard.press', key]);
      if (this.behaviour.failOn.has('keyboard.press')) throw new Error(`Unknown key: "${key}"`);
    },
  };

  shot(type: 'png' | 'jpeg'): Uint8Array {
    return type === 'png' ? bytesWithMagic(PNG_MAGIC, this.behaviour.pngSize) : bytesWithMagic(JPEG_MAGIC, this.behaviour.jpegSize);
  }

  emit(event: string, arg: unknown): void {
    for (const l of this.listeners.get(event) ?? []) l(arg);
  }

  async goto(url: string, options: { waitUntil: 'load'; timeout: number }): Promise<ResponseLike | null> {
    this.calls.push(['goto', url, options]);
    if (this.behaviour.failOn.has('goto')) throw new Error(`page.goto: net::ERR_CONNECTION_REFUSED at ${url}`);
    this.currentUrl = url;
    for (const e of this.behaviour.consoleErrorsOnGoto) this.emit('console', { type: () => 'error', text: () => e });
    this.emit('console', { type: () => 'log', text: () => 'just a log' });
    for (const r of this.behaviour.failedResponsesOnGoto) {
      this.emit('response', { status: () => r.status, url: () => r.url, request: () => ({ method: () => 'GET' }) });
    }
    const status = this.behaviour.status;
    return { status: () => status };
  }
  url(): string {
    return this.currentUrl;
  }
  async title(): Promise<string> {
    return this.behaviour.title;
  }
  locator(selector: string): LocatorLike {
    return new FakeLocator(this, `locator(${selector})`);
  }
  getByText(text: string): LocatorLike {
    return new FakeLocator(this, `text(${text})`);
  }
  getByRole(role: string, options?: { name?: string }): LocatorLike {
    return new FakeLocator(this, `role(${role}${options?.name ? `,${options.name}` : ''})`);
  }
  getByLabel(label: string): LocatorLike {
    return new FakeLocator(this, `label(${label})`);
  }
  async screenshot(options: ScreenshotOptions): Promise<Uint8Array> {
    this.calls.push(['screenshot', options]);
    if (this.behaviour.failOn.has('screenshot')) throw new Error('page.screenshot: Target closed');
    return this.shot(options.type);
  }
  async waitForURL(predicate: (url: URL) => boolean, options: { timeout: number }): Promise<void> {
    this.calls.push(['waitForURL', options]);
    if (!predicate(new URL(this.currentUrl === 'about:blank' ? 'http://blank.invalid/' : this.currentUrl))) {
      throw new Error(`page.waitForURL: Timeout ${options.timeout}ms exceeded.`);
    }
  }
  async setViewportSize(viewport: Viewport): Promise<void> {
    this.calls.push(['setViewportSize', viewport]);
    this.viewport = viewport;
  }
  video(): VideoLike | null {
    const ctx = this.context;
    if (!ctx.options.recordVideo) return null;
    return { path: async () => ctx.videoPath };
  }
  on(event: string, listener: (arg: unknown) => void): unknown {
    const list = this.listeners.get(event) ?? [];
    list.push(listener);
    this.listeners.set(event, list);
    return this;
  }
  async close(): Promise<void> {
    this.closed = true;
  }
}

let videoCounter = 0;

export class FakeContext implements ContextLike {
  readonly pages: FakePage[] = [];
  closed = false;
  readonly videoPath: string;

  constructor(
    readonly behaviour: FakeBehaviour,
    readonly options: ContextOptions,
  ) {
    videoCounter += 1;
    this.videoPath = options.recordVideo ? path.join(options.recordVideo.dir, `video-${videoCounter}.webm`) : '';
  }

  async newPage(): Promise<PageLike> {
    const page = new FakePage(this.behaviour, this);
    this.pages.push(page);
    return page;
  }

  async close(): Promise<void> {
    this.closed = true;
    if (this.options.recordVideo) {
      mkdirSync(this.options.recordVideo.dir, { recursive: true });
      writeFileSync(this.videoPath, bytesWithMagic(WEBM_MAGIC, this.behaviour.videoSize));
    }
  }
}

export class FakeBrowser implements BrowserLike {
  readonly contexts: FakeContext[] = [];
  connected = true;

  constructor(readonly behaviour: FakeBehaviour) {}

  async newContext(options: ContextOptions): Promise<ContextLike> {
    const ctx = new FakeContext(this.behaviour, options);
    this.contexts.push(ctx);
    return ctx;
  }
  async close(): Promise<void> {
    this.connected = false;
  }
  isConnected(): boolean {
    return this.connected;
  }

  get lastContext(): FakeContext {
    const c = this.contexts[this.contexts.length - 1];
    if (!c) throw new Error('no context');
    return c;
  }

  get page(): FakePage {
    const p = this.lastContext.pages[this.lastContext.pages.length - 1];
    if (!p) throw new Error('no page');
    return p;
  }
}

export class FakeLauncher implements BrowserLauncher {
  readonly launches: LaunchOptions[] = [];
  readonly browsers: FakeBrowser[] = [];
  readonly behaviour: FakeBehaviour;

  constructor(overrides: Partial<FakeBehaviour> = {}) {
    this.behaviour = {
      status: 200,
      title: 'Fake App',
      pngSize: 1024,
      jpegSize: 512,
      videoSize: 2048,
      innerText: 'Hello from the fake page',
      failOn: new Set(),
      consoleErrorsOnGoto: [],
      failedResponsesOnGoto: [],
      ...overrides,
    };
  }

  async launch(options: LaunchOptions): Promise<BrowserLike> {
    this.launches.push(options);
    if (this.behaviour.launchError) throw new Error(this.behaviour.launchError);
    const b = new FakeBrowser(this.behaviour);
    this.browsers.push(b);
    return b;
  }

  get browser(): FakeBrowser {
    const b = this.browsers[this.browsers.length - 1];
    if (!b) throw new Error('not launched');
    return b;
  }
}
