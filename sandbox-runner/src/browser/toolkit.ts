// Handlers for the browser-testing MCP tools. src/mcp/tools/browser.ts registers them with track C's
// MCP server; this module holds the behaviour so it can be tested without an MCP server.
import { mkdir, readFile, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';

import { AppManager } from './app.js';
import { ArgError, labelAndGroup, optBool, optInt, optString, optViewport, reqString } from './args.js';
import { DEFAULT_OUTPUT_DIR, DEFAULT_REPO_DIR, homeDir } from './config.js';
import { readHead, resolveAllowedFile, sniffContentType } from './files.js';
import { loadChromium } from './playwright.js';
import { ACTION_TIMEOUT_MS, BrowserSession, type FinishedRecording } from './session.js';
import type { BrowserToolOptions, LocatorLike, PageLike, ToolContent, ToolHandler, ToolResult } from './types.js';
import { ArtifactUploader, tooLargeMessage, type ArtifactMime, type UploadedArtifact } from './upload.js';
import {
  Mutex,
  ToolError,
  cleanDriverError,
  fail,
  formatBytes,
  ok,
  slugify,
  text,
  truncate,
  withTimeout,
} from './util.js';

/** Images larger than this are not echoed back inline (model image inputs are capped around 5 MB base64). */
export const INLINE_IMAGE_MAX_BYTES = 3.5 * 1024 * 1024;
const SCREENSHOT_TIMEOUT_MS = 30_000;
const JPEG_QUALITY = 80;
const RECENT_DIAGNOSTICS = 20;

type Args = Record<string, unknown>;

export class BrowserToolkit {
  readonly session: BrowserSession;
  readonly app: AppManager;
  readonly uploader: ArtifactUploader;
  private readonly outputDir: string;
  private readonly repoDir: string;
  private readonly allowedRoots: string[];
  private readonly browserLock = new Mutex();
  private readonly appLock = new Mutex();

  constructor(options: BrowserToolOptions = {}) {
    const env = options.env ?? process.env;
    this.outputDir = options.outputDir ?? DEFAULT_OUTPUT_DIR;
    this.repoDir = options.repoDir ?? DEFAULT_REPO_DIR;
    this.allowedRoots = options.allowedRoots ?? ['/workspace', '/tmp', homeDir(env)];
    this.session = new BrowserSession({
      env,
      outputDir: this.outputDir,
      loadLauncher: options.loadLauncher ?? (() => loadChromium(env)),
    });
    const appOpts: ConstructorParameters<typeof AppManager>[0] = { env, repoDir: this.repoDir };
    if (options.defaultAppPort !== undefined) appOpts.defaultPort = options.defaultAppPort;
    this.app = new AppManager(appOpts);
    const upOpts: ConstructorParameters<typeof ArtifactUploader>[0] = { env };
    if (options.fetch) upOpts.fetch = options.fetch;
    if (options.sleep) upOpts.sleep = options.sleep;
    this.uploader = new ArtifactUploader(upOpts);
  }

  /** Wraps a handler so it never throws: every failure becomes an `isError` text result. */
  static safe(name: string, fn: (args: Args) => Promise<ToolResult>): ToolHandler {
    return async (args) => {
      try {
        const input = args && typeof args === 'object' && !Array.isArray(args) ? args : {};
        return await fn(input);
      } catch (err) {
        if (err instanceof ToolError) return fail(err.message);
        return fail(`${name} failed: ${cleanDriverError(err)}`);
      }
    };
  }

  // ------------------------------------------------------------------ app lifecycle

  startApp(args: Args): Promise<ToolResult> {
    const command = optString(args, 'command', { max: 4000 });
    const url = optString(args, 'url', { max: 2000 });
    const cwd = optString(args, 'cwd', { max: 1000 });
    const port = optInt(args, 'port', 1, 65535);
    const timeoutS = optInt(args, 'readyTimeoutSeconds', 1, 900) ?? 120;
    return this.appLock.run(async () => {
      const input: Parameters<AppManager['start']>[0] = {};
      if (command) input.command = command;
      if (url) input.url = url;
      if (cwd) input.cwd = cwd;
      if (port !== undefined) input.port = port;
      const r = await this.app.start(input, timeoutS * 1000);
      return ok(
        [
          `App is ${r.alreadyRunning ? 'already running' : 'running'} at ${r.url}`,
          `Command: \`${r.command}\` (from ${r.source}) in ${r.cwd}`,
          'Open it with browser_open (relative paths like "/settings" resolve against this URL).',
          r.logTail ? `\nRecent output:\n${r.logTail}` : '',
        ]
          .filter(Boolean)
          .join('\n'),
      );
    });
  }

  stopApp(): Promise<ToolResult> {
    return this.appLock.run(async () => {
      const r = await this.app.stop();
      if (!r.stopped) return ok('No app is running.');
      return ok(`Stopped the app.${r.logTail ? `\n\nLast output:\n${r.logTail}` : ''}`);
    });
  }

  // ------------------------------------------------------------------ navigation

  open(args: Args): Promise<ToolResult> {
    const rawUrl = reqString(args, 'url', { max: 4000 });
    const viewport = optViewport(args);
    const record = optBool(args, 'record') ?? false;
    const url = resolveTargetUrl(rawUrl, this.app.url);
    return this.browserLock.run(async () => {
      const s = this.session;
      if (viewport) await s.setViewport(viewport);
      if (record && !s.recording) await s.startRecording({ label: 'recording', viewport: s.viewport });
      const cursor = { console: s.consoleErrors.count, requests: s.failedRequests.count };
      const status = await s.navigate(url);
      const page = s.requirePage();
      const title = await safeTitle(page);
      const lines = [
        `Opened ${page.url()}`,
        `Title: ${title || '(none)'}`,
        `HTTP status: ${status ?? 'n/a'}${status !== undefined && status >= 400 ? ' (error page)' : ''}`,
      ];
      if (s.recording) lines.push(`Recording "${s.recording.label}" is in progress.`);
      lines.push(...this.diagnostics(cursor));
      return ok(lines.join('\n'));
    });
  }

  click(args: Args): Promise<ToolResult> {
    return this.browserLock.run(async () => {
      const page = this.session.requirePage();
      const { locator, description } = resolveLocator(page, args, { allowText: true, allowLabel: false });
      const cursor = this.cursor();
      await driver(locator.click({ timeout: ACTION_TIMEOUT_MS }), `Clicking ${description}`);
      return ok([`Clicked ${description}.`, ...(await this.pageSummary(page)), ...this.diagnostics(cursor)].join('\n'));
    });
  }

  type(args: Args): Promise<ToolResult> {
    const value = reqString(args, 'text', { max: 20_000, allowEmpty: true });
    const submit = optBool(args, 'submit') ?? false;
    const clear = optBool(args, 'clear') ?? true;
    return this.browserLock.run(async () => {
      const page = this.session.requirePage();
      const { locator, description } = resolveLocator(page, args, { allowText: false, allowLabel: true });
      const cursor = this.cursor();
      if (clear) {
        await driver(locator.fill(value, { timeout: ACTION_TIMEOUT_MS }), `Filling ${description}`);
      } else {
        await driver(locator.pressSequentially(value, { timeout: ACTION_TIMEOUT_MS }), `Typing into ${description}`);
      }
      if (submit) await driver(locator.press('Enter', { timeout: ACTION_TIMEOUT_MS }), `Pressing Enter in ${description}`);
      const lines = [`Typed ${value.length} characters into ${description}${submit ? ' and pressed Enter' : ''}.`];
      lines.push(...(await this.pageSummary(page)), ...this.diagnostics(cursor));
      return ok(lines.join('\n'));
    });
  }

  press(args: Args): Promise<ToolResult> {
    const key = reqString(args, 'key', { max: 64 });
    return this.browserLock.run(async () => {
      const page = this.session.requirePage();
      const cursor = this.cursor();
      await driver(page.keyboard.press(key), `Pressing ${key}`);
      return ok([`Pressed ${key}.`, ...(await this.pageSummary(page)), ...this.diagnostics(cursor)].join('\n'));
    });
  }

  waitFor(args: Args): Promise<ToolResult> {
    const selector = optString(args, 'selector', { max: 2000 });
    const textArg = optString(args, 'text', { max: 2000 });
    const urlIncludes = optString(args, 'urlIncludes', { max: 2000 });
    const timeoutMs = (optInt(args, 'timeoutSeconds', 1, 120) ?? 15) * 1000;
    const given = [selector, textArg, urlIncludes].filter((v) => v !== undefined).length;
    if (given !== 1) throw new ArgError('pass exactly one of "selector", "text" or "urlIncludes"');
    return this.browserLock.run(async () => {
      const page = this.session.requirePage();
      let what: string;
      let op: Promise<void>;
      if (selector) {
        what = `selector ${JSON.stringify(selector)} to be visible`;
        op = page.locator(selector).first().waitFor({ state: 'visible', timeout: timeoutMs });
      } else if (textArg) {
        what = `text ${JSON.stringify(textArg)} to be visible`;
        op = page.getByText(textArg).first().waitFor({ state: 'visible', timeout: timeoutMs });
      } else {
        const needle = urlIncludes as string;
        what = `URL to include ${JSON.stringify(needle)}`;
        op = page.waitForURL((u) => u.toString().includes(needle), { timeout: timeoutMs });
      }
      try {
        await withTimeout(op, timeoutMs + 5000, `Waiting for ${what}`);
      } catch (err) {
        throw new ToolError(`Timed out after ${timeoutMs / 1000}s waiting for ${what}. ${cleanDriverError(err)}\nCurrent URL: ${page.url()}`);
      }
      return ok([`Done waiting for ${what}.`, ...(await this.pageSummary(page))].join('\n'));
    });
  }

  read(args: Args): Promise<ToolResult> {
    const selector = optString(args, 'selector', { max: 2000 });
    const maxChars = optInt(args, 'maxChars', 100, 100_000) ?? 8000;
    return this.browserLock.run(async () => {
      const page = this.session.requirePage();
      const locator = page.locator(selector ?? 'body').first();
      const visible = await driver(locator.innerText({ timeout: ACTION_TIMEOUT_MS }), `Reading ${selector ? `selector ${JSON.stringify(selector)}` : 'the page'}`);
      const s = this.session;
      const errors = s.consoleErrors.tail(RECENT_DIAGNOSTICS);
      const requests = s.failedRequests.tail(RECENT_DIAGNOSTICS);
      const lines = [
        ...(await this.pageSummary(page)),
        `--- Visible text${selector ? ` of ${selector}` : ''} ---`,
        truncate(visible.trim(), maxChars) || '(no visible text)',
        `--- Recent console errors (${errors.length}) ---`,
        errors.length ? errors.join('\n') : '(none)',
        `--- Failed requests (${requests.length}) ---`,
        requests.length ? requests.join('\n') : '(none)',
      ];
      return ok(lines.join('\n'));
    });
  }

  // ------------------------------------------------------------------ captures

  screenshot(args: Args): Promise<ToolResult> {
    const { label, group } = labelAndGroup(args);
    const fullPage = optBool(args, 'fullPage') ?? false;
    const selector = optString(args, 'selector', { max: 2000 });
    const upload = optBool(args, 'upload') ?? true;
    return this.browserLock.run(async () => {
      const page = this.session.requirePage();
      const max = this.uploader.maxBytes;
      const capture = (type: 'png' | 'jpeg'): Promise<Uint8Array> => {
        const base = type === 'jpeg' ? { type, quality: JPEG_QUALITY, timeout: SCREENSHOT_TIMEOUT_MS } : { type, timeout: SCREENSHOT_TIMEOUT_MS };
        const op = selector ? page.locator(selector).first().screenshot(base) : page.screenshot({ ...base, fullPage });
        return driver(op, 'Taking the screenshot', SCREENSHOT_TIMEOUT_MS + 5000);
      };
      let bytes = await capture('png');
      let mime: ArtifactMime = 'image/png';
      let ext = 'png';
      if (bytes.byteLength > max) {
        bytes = await capture('jpeg');
        mime = 'image/jpeg';
        ext = 'jpg';
        if (bytes.byteLength > max) throw new ToolError(tooLargeMessage('screenshot', bytes.byteLength, max));
      }
      const file = await this.save(bytes, label, ext);
      const image = inlineImage(bytes, mime);
      const where = `Page: ${page.url()}\nSaved to ${file} (${formatBytes(bytes.byteLength)}${mime === 'image/jpeg' ? ', JPEG because the PNG exceeded the size limit' : ''}).`;
      if (!upload) return ok(`Screenshot "${label}" captured (not uploaded).\n${where}`, ...image);
      let artifact: UploadedArtifact;
      try {
        artifact = await this.uploader.upload({ bytes, filename: `${slugify(label)}.${ext}`, contentType: mime, kind: 'screenshot', label, ...(group ? { group } : {}) });
      } catch (err) {
        const reason = err instanceof ToolError ? err.message : cleanDriverError(err);
        return fail(`Screenshot "${label}" was captured but not uploaded: ${reason}\n${where}`, ...image);
      }
      return ok(
        `Screenshot "${label}"${group ? ` (group "${group}")` : ''} uploaded as artifact ${artifact.id}; it is attached to the session and the PR.\n${where}`,
        ...image,
      );
    });
  }

  recordStart(args: Args): Promise<ToolResult> {
    const { label, group } = labelAndGroup(args);
    const viewport = optViewport(args);
    return this.browserLock.run(async () => {
      const s = this.session;
      const r = await s.startRecording({ label, viewport: viewport ?? s.viewport, ...(group ? { group } : {}) });
      return ok(
        `Recording "${label}" started at ${s.viewport.width}x${s.viewport.height}.${
          r.reopenedUrl ? ` Reopened ${r.reopenedUrl} in the recording context.` : ' No page was open: navigate with browser_open.'
        } Perform the flow, then call browser_record_stop to upload the video.`,
      );
    });
  }

  recordStop(): Promise<ToolResult> {
    return this.browserLock.run(async () => {
      const finished = await this.session.stopRecording({ reopen: true });
      if (!finished) return fail('No recording is in progress. Call browser_record_start first.');
      return this.uploadRecording(finished);
    });
  }

  close(): Promise<ToolResult> {
    return this.browserLock.run(async () => {
      let videoResult: ToolResult | undefined;
      if (this.session.recording) {
        try {
          const finished = await this.session.stopRecording({ reopen: false });
          if (finished) videoResult = await this.uploadRecording(finished);
        } catch (err) {
          videoResult = fail(err instanceof ToolError ? err.message : `Finalizing the recording failed: ${cleanDriverError(err)}`);
        }
      }
      await this.session.close();
      if (!videoResult) return ok('Browser closed.');
      return { content: [text('Browser closed.'), ...videoResult.content], ...(videoResult.isError ? { isError: true } : {}) };
    });
  }

  async attach(args: Args): Promise<ToolResult> {
    const requested = reqString(args, 'path', { max: 4096 });
    const { label, group } = labelAndGroup(args);
    const file = await resolveAllowedFile(requested, this.repoDir, this.allowedRoots);
    const sniffed = sniffContentType(await readHead(file));
    if (!sniffed) throw new ToolError(`${file} is not a PNG, JPEG, WebM or MP4 file; only those can be attached.`);
    const size = (await stat(file)).size;
    const max = this.uploader.maxBytes;
    if (size > max) throw new ToolError(tooLargeMessage(sniffed.kind, size, max));
    const bytes = new Uint8Array(await readFile(file));
    const artifact = await this.uploader.upload({
      bytes,
      filename: `${slugify(path.basename(file, path.extname(file)))}.${sniffed.extension}`,
      contentType: sniffed.contentType,
      kind: sniffed.kind,
      label,
      ...(group ? { group } : {}),
    });
    const image = sniffed.kind === 'screenshot' ? inlineImage(bytes, sniffed.contentType) : [];
    return ok(
      `Attached ${file} (${sniffed.contentType}, ${formatBytes(size)}) as ${sniffed.kind} artifact ${artifact.id} labelled "${label}"${
        group ? ` in group "${group}"` : ''
      }; it is attached to the session and the PR.`,
      ...image,
    );
  }

  // ------------------------------------------------------------------ helpers

  private async uploadRecording(finished: FinishedRecording): Promise<ToolResult> {
    let size: number;
    try {
      size = (await stat(finished.path)).size;
    } catch {
      return fail(`The recorded video file is missing (${finished.path}).`);
    }
    const max = this.uploader.maxBytes;
    if (size > max) return fail(`${tooLargeMessage('video', size, max)} The file is at ${finished.path}.`);
    const seconds = Math.round((Date.now() - finished.startedAt) / 1000);
    try {
      const artifact = await this.uploader.upload({
        bytes: new Uint8Array(await readFile(finished.path)),
        filename: `${slugify(finished.label)}.webm`,
        contentType: 'video/webm',
        kind: 'video',
        label: finished.label,
        ...(finished.group ? { group: finished.group } : {}),
      });
      return ok(
        `Video "${finished.label}" (~${seconds}s, ${formatBytes(size)}) uploaded as artifact ${artifact.id}; it is attached to the session and the PR. Saved to ${finished.path}.`,
      );
    } catch (err) {
      return fail(`Video "${finished.label}" was recorded to ${finished.path} but not uploaded: ${err instanceof ToolError ? err.message : cleanDriverError(err)}`);
    }
  }

  private async save(bytes: Uint8Array, label: string, ext: string): Promise<string> {
    await mkdir(this.outputDir, { recursive: true });
    const stamp = new Date().toISOString().replace(/[:.]/g, '-');
    const file = path.join(this.outputDir, `${stamp}-${slugify(label)}.${ext}`);
    await writeFile(file, bytes);
    return file;
  }

  private cursor(): { console: number; requests: number } {
    return { console: this.session.consoleErrors.count, requests: this.session.failedRequests.count };
  }

  private diagnostics(cursor: { console: number; requests: number }): string[] {
    const errors = this.session.consoleErrors.since(cursor.console).slice(-RECENT_DIAGNOSTICS);
    const requests = this.session.failedRequests.since(cursor.requests).slice(-RECENT_DIAGNOSTICS);
    const out: string[] = [];
    out.push(errors.length ? `Console errors:\n${errors.map((e) => `- ${e}`).join('\n')}` : 'Console errors: none');
    if (requests.length) out.push(`Failed requests:\n${requests.map((r) => `- ${r}`).join('\n')}`);
    return out;
  }

  private async pageSummary(page: PageLike): Promise<string[]> {
    return [`URL: ${page.url()}`, `Title: ${(await safeTitle(page)) || '(none)'}`];
  }
}

async function driver<T>(op: Promise<T>, label: string, ms = ACTION_TIMEOUT_MS + 5000): Promise<T> {
  try {
    return await withTimeout(op, ms, label);
  } catch (err) {
    if (err instanceof ToolError) throw err;
    throw new ToolError(`${label} failed: ${cleanDriverError(err)}`);
  }
}

async function safeTitle(page: PageLike): Promise<string> {
  try {
    return await withTimeout(page.title(), 5000, 'Reading the title');
  } catch {
    return '';
  }
}

function inlineImage(bytes: Uint8Array, mime: string): ToolContent[] {
  if (bytes.byteLength > INLINE_IMAGE_MAX_BYTES) {
    return [text(`(Image is ${formatBytes(bytes.byteLength)}, too large to show inline; read the saved file instead.)`)];
  }
  return [{ type: 'image', data: Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength).toString('base64'), mimeType: mime }];
}

/**
 * Resolves a browser_open URL: full http(s) URLs as-is, "host:port/…" as http, and relative paths
 * against the started app. Any other scheme (file:, chrome:, data:, javascript:, about:) is refused.
 */
export function resolveTargetUrl(raw: string, appUrl: string | undefined): string {
  const value = raw.trim();
  if (/^(localhost|127(?:\.\d{1,3}){3}|0\.0\.0\.0|\[[0-9a-f:]+\]|[a-z0-9-]+(?:\.[a-z0-9-]+)+):\d{1,5}(?:[/?#]|$)/i.test(value)) {
    return new URL(`http://${value}`).toString();
  }
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(value);
  if (scheme) {
    const proto = scheme[1]!.toLowerCase();
    if (proto !== 'http' && proto !== 'https') {
      throw new ToolError(`Only http(s) URLs can be opened; refusing "${proto}:" URLs.`);
    }
    try {
      return new URL(value).toString();
    } catch {
      throw new ToolError(`Not a valid URL: ${value}`);
    }
  }
  if (value.startsWith('//')) throw new ToolError(`Use a full http(s) URL instead of "${value}".`);
  if (!appUrl) {
    throw new ToolError(`"${value}" is a relative URL but no app is running: call browser_start_app first or pass a full http(s) URL.`);
  }
  return new URL(value, `${appUrl.replace(/\/+$/, '')}/`).toString();
}

function resolveLocator(
  page: PageLike,
  args: Args,
  opts: { allowText: boolean; allowLabel: boolean },
): { locator: LocatorLike; description: string } {
  const selector = optString(args, 'selector', { max: 2000 });
  const textArg = opts.allowText ? optString(args, 'text', { max: 2000 }) : undefined;
  const role = optString(args, 'role', { max: 64 });
  const name = optString(args, 'name', { max: 2000 });
  const label = opts.allowLabel ? optString(args, 'label', { max: 2000 }) : undefined;
  const given = [selector, textArg, role, label].filter((v) => v !== undefined).length;
  const options = [
    '"selector"',
    ...(opts.allowText ? ['"text"'] : []),
    '"role" (+ "name")',
    ...(opts.allowLabel ? ['"label"'] : []),
  ].join(', ');
  if (given !== 1) throw new ArgError(`pass exactly one of ${options}`);
  if (name !== undefined && role === undefined) throw new ArgError('"name" is only used together with "role"');
  if (selector) return { locator: page.locator(selector).first(), description: `selector ${JSON.stringify(selector)}` };
  if (textArg) return { locator: page.getByText(textArg).first(), description: `text ${JSON.stringify(textArg)}` };
  if (label) return { locator: page.getByLabel(label).first(), description: `field labelled ${JSON.stringify(label)}` };
  const r = role as string;
  return {
    locator: (name ? page.getByRole(r, { name }) : page.getByRole(r)).first(),
    description: name ? `${r} ${JSON.stringify(name)}` : `first ${r}`,
  };
}
