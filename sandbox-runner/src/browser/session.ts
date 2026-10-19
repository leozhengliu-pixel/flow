// Shared browser state for all browser_* tools: one headless Chromium, one context, one page, plus
// the active video recording. Every method is safe to call in any order: missing pieces are created
// lazily and a crashed browser is relaunched.
import { mkdir } from 'node:fs/promises';
import path from 'node:path';

import { DEFAULT_VIEWPORT } from './config.js';
import { chromiumLaunchOptions } from './playwright.js';
import type { BrowserLauncher, BrowserLike, ContextLike, ContextOptions, PageLike, Viewport } from './types.js';
import { RingBuffer, ToolError, cleanDriverError, withTimeout } from './util.js';

export const LAUNCH_TIMEOUT_MS = 60_000;
export const NAV_TIMEOUT_MS = 30_000;
export const ACTION_TIMEOUT_MS = 15_000;
export const CLOSE_TIMEOUT_MS = 15_000;
/** Closing a recording context encodes the video, which can take a while. */
export const VIDEO_FINALIZE_TIMEOUT_MS = 60_000;

export interface Recording {
  label: string;
  group?: string;
  viewport: Viewport;
  startedAt: number;
}

export interface FinishedRecording extends Recording {
  path: string;
  /** URL the page was on when recording stopped (used to reopen). */
  url: string;
}

export interface BrowserSessionOptions {
  env: NodeJS.ProcessEnv;
  loadLauncher: () => Promise<BrowserLauncher>;
  outputDir: string;
}

export class BrowserSession {
  private browser: BrowserLike | undefined;
  private context: ContextLike | undefined;
  private page: PageLike | undefined;
  private launcher: Promise<BrowserLauncher> | undefined;
  private recordingInfo: Recording | undefined;
  viewport: Viewport = { ...DEFAULT_VIEWPORT };
  readonly consoleErrors = new RingBuffer<string>(100);
  readonly failedRequests = new RingBuffer<string>(100);

  constructor(private readonly opts: BrowserSessionOptions) {}

  get videoDir(): string {
    return path.join(this.opts.outputDir, 'videos');
  }

  get recording(): Recording | undefined {
    return this.recordingInfo;
  }

  get hasPage(): boolean {
    return !!this.page && !!this.browser?.isConnected();
  }

  /** The open page, or a clear error telling the agent to open one first. */
  requirePage(): PageLike {
    if (!this.page || !this.browser?.isConnected()) {
      throw new ToolError('No page is open. Call browser_open with a URL first.');
    }
    return this.page;
  }

  /** URL of the current page if it is an http(s) page. */
  currentHttpUrl(): string | undefined {
    if (!this.page) return undefined;
    try {
      const u = this.page.url();
      return /^https?:\/\//i.test(u) ? u : undefined;
    } catch {
      return undefined;
    }
  }

  /** Returns the page, launching Chromium and creating a context as needed. */
  async ensurePage(): Promise<PageLike> {
    if (this.page && this.browser?.isConnected()) return this.page;
    await this.ensureBrowser();
    await this.newContext(this.recordingInfo ? { recordDir: this.videoDir } : {});
    return this.requirePage();
  }

  async setViewport(viewport: Viewport): Promise<void> {
    this.viewport = viewport;
    if (this.page) await withTimeout(this.page.setViewportSize(viewport), ACTION_TIMEOUT_MS, 'Resizing the viewport');
  }

  /** Navigates and returns the HTTP status (undefined for same-document navigations). */
  async navigate(url: string): Promise<number | undefined> {
    const page = await this.ensurePage();
    try {
      const res = await withTimeout(
        page.goto(url, { waitUntil: 'load', timeout: NAV_TIMEOUT_MS }),
        NAV_TIMEOUT_MS + 5000,
        `Navigating to ${url}`,
      );
      return res ? res.status() : undefined;
    } catch (err) {
      throw new ToolError(`Could not open ${url}: ${cleanDriverError(err)}`);
    }
  }

  async startRecording(rec: Omit<Recording, 'startedAt'>): Promise<{ reopenedUrl?: string }> {
    if (this.recordingInfo) {
      throw new ToolError(`A recording ("${this.recordingInfo.label}") is already in progress. Call browser_record_stop first.`);
    }
    const url = this.currentHttpUrl();
    this.viewport = rec.viewport;
    await this.ensureBrowser();
    await mkdir(this.videoDir, { recursive: true });
    this.recordingInfo = { ...rec, startedAt: Date.now() };
    try {
      await this.newContext({ recordDir: this.videoDir });
      if (url) await this.navigate(url);
    } catch (err) {
      this.recordingInfo = undefined;
      throw err;
    }
    return url ? { reopenedUrl: url } : {};
  }

  /**
   * Closes the recording context so Playwright finalizes the video, then (optionally) reopens a
   * normal context on the same URL. Returns undefined when nothing was being recorded.
   */
  async stopRecording(opts: { reopen: boolean }): Promise<FinishedRecording | undefined> {
    const rec = this.recordingInfo;
    if (!rec) return undefined;
    this.recordingInfo = undefined;
    const page = this.page;
    const context = this.context;
    const url = this.currentHttpUrl() ?? '';
    const video = page?.video() ?? null;
    this.page = undefined;
    this.context = undefined;
    if (!context || !video) {
      throw new ToolError('The recording was lost (the browser closed or crashed); no video is available.');
    }
    await withTimeout(context.close(), VIDEO_FINALIZE_TIMEOUT_MS, 'Finalizing the video');
    const videoPath = await withTimeout(video.path(), CLOSE_TIMEOUT_MS, 'Locating the recorded video');
    if (opts.reopen && this.browser?.isConnected()) {
      await this.newContext({});
      if (url) {
        try {
          await this.navigate(url);
        } catch {
          // Reopening is best effort; the video is what matters here.
        }
      }
    }
    return { ...rec, path: videoPath, url };
  }

  /** Closes the browser. Call stopRecording first to keep the video. */
  async close(): Promise<void> {
    const browser = this.browser;
    this.browser = undefined;
    this.context = undefined;
    this.page = undefined;
    this.recordingInfo = undefined;
    if (browser) {
      try {
        await withTimeout(browser.close(), CLOSE_TIMEOUT_MS, 'Closing the browser');
      } catch {
        // The process is torn down when the runner exits anyway.
      }
    }
  }

  private async ensureBrowser(): Promise<BrowserLike> {
    if (this.browser?.isConnected()) return this.browser;
    this.browser = undefined;
    this.context = undefined;
    this.page = undefined;
    if (!this.launcher) {
      this.launcher = this.opts.loadLauncher();
      this.launcher.catch(() => {
        this.launcher = undefined; // allow a retry after a failed load
      });
    }
    const launcher = await this.launcher;
    try {
      this.browser = await withTimeout(
        launcher.launch(chromiumLaunchOptions(this.opts.env)),
        LAUNCH_TIMEOUT_MS + 5000,
        'Launching Chromium',
      );
    } catch (err) {
      throw new ToolError(`Could not launch headless Chromium: ${cleanDriverError(err)}`);
    }
    return this.browser;
  }

  private async newContext(opts: { recordDir?: string }): Promise<void> {
    const browser = await this.ensureBrowser();
    const old = this.context;
    this.context = undefined;
    this.page = undefined;
    if (old) {
      try {
        await withTimeout(old.close(), CLOSE_TIMEOUT_MS, 'Closing the previous browser context');
      } catch {
        // ignore; a fresh context is created regardless
      }
    }
    const options: ContextOptions = { viewport: this.viewport, ignoreHTTPSErrors: true };
    if (opts.recordDir) options.recordVideo = { dir: opts.recordDir, size: this.viewport };
    const context = await withTimeout(browser.newContext(options), ACTION_TIMEOUT_MS, 'Creating a browser context');
    const page = await withTimeout(context.newPage(), ACTION_TIMEOUT_MS, 'Opening a page');
    this.attachListeners(page);
    this.context = context;
    this.page = page;
  }

  private attachListeners(page: PageLike): void {
    page.on('console', (msg) => {
      const m = msg as { type?: () => string; text?: () => string };
      try {
        if (m.type?.() === 'error') this.consoleErrors.push(`console.error: ${m.text?.() ?? ''}`.slice(0, 1000));
      } catch {
        // ignore malformed events
      }
    });
    page.on('pageerror', (err) => {
      const message = err instanceof Error ? err.message : String(err);
      this.consoleErrors.push(`uncaught exception: ${message}`.slice(0, 1000));
    });
    page.on('requestfailed', (req) => {
      const r = req as { method?: () => string; url?: () => string; failure?: () => { errorText?: string } | null };
      try {
        this.failedRequests.push(`${r.method?.() ?? 'GET'} ${r.url?.() ?? '?'} failed: ${r.failure?.()?.errorText ?? 'unknown error'}`.slice(0, 1000));
      } catch {
        // ignore malformed events
      }
    });
    page.on('response', (res) => {
      const r = res as { status?: () => number; url?: () => string; request?: () => { method?: () => string } };
      try {
        const status = r.status?.() ?? 0;
        if (status >= 400) {
          this.failedRequests.push(`${r.request?.().method?.() ?? 'GET'} ${r.url?.() ?? '?'} → HTTP ${status}`.slice(0, 1000));
        }
      } catch {
        // ignore malformed events
      }
    });
  }
}
