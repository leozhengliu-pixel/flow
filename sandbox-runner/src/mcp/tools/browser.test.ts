import { mkdir, mkdtemp, readdir, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { FakeLauncher, JPEG_MAGIC, PNG_MAGIC, WEBM_MAGIC, bytesWithMagic, type FakeBehaviour } from '../../browser/testing.js';
import type { ToolHandler, ToolResult, ToolSchema } from '../../browser/types.js';
import { BROWSER_TOOL_NAMES, createBrowserTools, registerBrowserTools } from './browser.js';

const TOKEN = 'fcs_do_not_leak';

let base: string;
let env: NodeJS.ProcessEnv;

beforeEach(async () => {
  base = await realpath(await mkdtemp(path.join(tmpdir(), 'flow-browser-tools-')));
  await mkdir(path.join(base, 'repo'));
  await mkdir(path.join(base, 'home'));
  await writeFile(path.join(base, 'token'), TOKEN);
  env = {
    FLOW_RUNTIME_URL: 'http://flow.test',
    FLOW_TOKEN_FILE: path.join(base, 'token'),
    FLOW_TURN_ID: 'turn_9',
    HOME: path.join(base, 'home'),
    // The real-process test spawns `npm run dev`.
    ...(process.env.PATH ? { PATH: process.env.PATH } : {}),
  };
});

afterEach(async () => {
  await rm(base, { recursive: true, force: true });
});

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status });
}

interface Harness {
  tools: Map<string, { schema: ToolSchema; handler: ToolHandler }>;
  call: (name: string, args?: unknown) => Promise<ToolResult>;
  launcher: FakeLauncher;
  fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;
  outputDir: string;
}

function setup(behaviour: Partial<FakeBehaviour> = {}, opts: { fetch?: typeof fetch; loadError?: string } = {}): Harness {
  const tools = new Map<string, { schema: ToolSchema; handler: ToolHandler }>();
  const launcher = new FakeLauncher(behaviour);
  let n = 0;
  const fetchMock = vi.fn<typeof fetch>(opts.fetch ?? (async () => json(201, { id: `art_${++n}`, url: `https://flow.test/files/${n}` })));
  const outputDir = path.join(base, 'out');
  registerBrowserTools(
    (name, schema, handler) => {
      tools.set(name, { schema, handler });
    },
    {
      env,
      fetch: fetchMock,
      sleep: async () => undefined,
      outputDir,
      defaultAppPort: 5182,
      repoDir: path.join(base, 'repo'),
      allowedRoots: [path.join(base, 'repo'), path.join(base, 'home')],
      loadLauncher: async () => {
        if (opts.loadError) throw new Error(opts.loadError);
        return launcher;
      },
    },
  );
  return {
    tools,
    launcher,
    fetchMock,
    outputDir,
    call: (name, args = {}) => {
      const t = tools.get(name);
      if (!t) throw new Error(`tool ${name} not registered`);
      return t.handler(args as Record<string, unknown>);
    },
  };
}

function textOf(r: ToolResult): string {
  return r.content
    .filter((c): c is { type: 'text'; text: string } => c.type === 'text')
    .map((c) => c.text)
    .join('\n');
}

function formOf(h: Harness, i = 0): FormData {
  return h.fetchMock.mock.calls[i]![1]!.body as FormData;
}

describe('registration', () => {
  it('registers every tool with an object input schema', () => {
    const h = setup();
    expect([...h.tools.keys()]).toEqual([...BROWSER_TOOL_NAMES]);
    for (const { schema } of h.tools.values()) {
      expect(schema.description.length).toBeGreaterThan(20);
      expect(schema.inputSchema.type).toBe('object');
    }
    expect(h.tools.get('browser_screenshot')!.schema.inputSchema.required).toEqual(['label']);
    expect(h.tools.get('attach_screenshot')!.schema.inputSchema.required).toEqual(['path', 'label']);
  });

  it('registers nothing when disabled by bootstrap or by FLOW_BROWSER_ENABLED=0', () => {
    const register = vi.fn();
    registerBrowserTools(register, { enabled: false, env });
    expect(createBrowserTools(register, { env: { ...env, FLOW_BROWSER_ENABLED: '0' } })).toBeUndefined();
    expect(register).not.toHaveBeenCalled();
    createBrowserTools(register, { env: { ...env, FLOW_BROWSER_ENABLED: '1' } });
    expect(register).toHaveBeenCalledTimes(BROWSER_TOOL_NAMES.length);
  });
});

describe('error paths never throw', () => {
  it('page tools explain that a page must be opened first', async () => {
    const h = setup();
    const cases: Array<[string, Record<string, unknown>]> = [
      ['browser_click', { selector: 'button' }],
      ['browser_type', { selector: 'input', text: 'x' }],
      ['browser_press', { key: 'Enter' }],
      ['browser_wait_for', { text: 'Done' }],
      ['browser_read', {}],
      ['browser_screenshot', { label: 'after' }],
    ];
    for (const [name, args] of cases) {
      const r = await h.call(name, args);
      expect(r.isError, name).toBe(true);
      expect(textOf(r), name).toMatch(/No page is open\. Call browser_open/);
    }
    expect(h.launcher.launches).toHaveLength(0);
  });

  it('validates arguments with readable messages', async () => {
    const h = setup();
    const cases: Array<[string, unknown, RegExp]> = [
      ['browser_open', {}, /"url" is required/],
      ['browser_open', { url: 5 }, /"url" must be a string/],
      ['browser_open', { url: '/', viewport: { width: 10, height: 10 } }, /"width" must be between/],
      ['browser_open', { url: '/', record: 'yes' }, /"record" must be true or false/],
      ['browser_screenshot', { label: 'x'.repeat(121) }, /"label" must be at most 120/],
      ['browser_screenshot', { label: 'a', group: 'g'.repeat(81) }, /"group" must be at most 80/],
      ['browser_screenshot', {}, /"label" is required/],
      ['browser_start_app', { port: 0 }, /"port" must be between 1 and 65535/],
      ['browser_start_app', { readyTimeoutSeconds: 1.5 }, /must be an integer/],
      ['browser_wait_for', { text: 'a', selector: 'b' }, /exactly one of/],
      ['browser_wait_for', {}, /exactly one of/],
      ['browser_press', {}, /"key" is required/],
      ['browser_read', { maxChars: 5 }, /"maxChars" must be between/],
      ['browser_record_start', {}, /"label" is required/],
      ['attach_screenshot', { label: 'x' }, /"path" is required/],
      ['browser_open', null, /"url" is required/],
      ['browser_open', ['x'], /"url" is required/],
    ];
    for (const [name, args, re] of cases) {
      const r = await h.call(name, args);
      expect(r.isError, `${name} ${JSON.stringify(args)}`).toBe(true);
      expect(textOf(r), `${name} ${JSON.stringify(args)}`).toMatch(re);
    }
  });

  it('reports a missing Playwright install', async () => {
    const h = setup({}, { loadError: 'Playwright is not available in this sandbox' });
    const r = await h.call('browser_open', { url: 'http://localhost:1234' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/browser_open failed: Playwright is not available/);
  });

  it('reports a Chromium launch failure', async () => {
    const h = setup({ launchError: 'browserType.launch: Executable does not exist at /opt/ms-playwright/chromium' });
    const r = await h.call('browser_open', { url: 'http://localhost:1234' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Could not launch headless Chromium: browserType.launch: Executable does not exist/);
  });

  it('stop/close/record_stop are safe with nothing running', async () => {
    const h = setup();
    expect(textOf(await h.call('browser_stop_app'))).toBe('No app is running.');
    expect(textOf(await h.call('browser_close'))).toBe('Browser closed.');
    const r = await h.call('browser_record_stop');
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/No recording is in progress/);
  });

  it('browser_start_app reports detection failures', async () => {
    const h = setup();
    const r = await h.call('browser_start_app');
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Could not determine how to start the app/);
  });
});

describe('browser_open', () => {
  it('launches once with sandbox-safe options and reports status, title and console errors', async () => {
    const h = setup({
      consoleErrorsOnGoto: ['TypeError: x is undefined'],
      failedResponsesOnGoto: [{ url: 'http://localhost:1234/api/me', status: 500 }],
    });
    const r = await h.call('browser_open', { url: 'http://localhost:1234/login' });
    expect(r.isError).toBeFalsy();
    const t = textOf(r);
    expect(t).toContain('Opened http://localhost:1234/login');
    expect(t).toContain('Title: Fake App');
    expect(t).toContain('HTTP status: 200');
    expect(t).toContain('console.error: TypeError: x is undefined');
    expect(t).not.toContain('just a log');
    expect(t).toContain('GET http://localhost:1234/api/me → HTTP 500');
    expect(h.launcher.launches).toEqual([expect.objectContaining({ headless: true, chromiumSandbox: false })]);
    expect(h.launcher.launches[0]!.args).toContain('--disable-dev-shm-usage');

    await h.call('browser_open', { url: 'localhost:1234/next', viewport: { width: 800, height: 600 } });
    expect(h.launcher.launches).toHaveLength(1);
    expect(h.launcher.browser.page.calls).toContainEqual(['setViewportSize', { width: 800, height: 600 }]);
    expect(h.launcher.browser.page.url()).toBe('http://localhost:1234/next');
  });

  it('marks error statuses', async () => {
    const h = setup({ status: 404 });
    expect(textOf(await h.call('browser_open', { url: 'http://127.0.0.1:9/x' }))).toContain('HTTP status: 404 (error page)');
  });

  it('refuses non-http schemes', async () => {
    const h = setup();
    for (const url of ['file:///etc/passwd', 'chrome://settings', 'data:text/html,hi', 'javascript:alert(1)', 'about:blank']) {
      const r = await h.call('browser_open', { url });
      expect(r.isError, url).toBe(true);
      expect(textOf(r)).toMatch(/Only http\(s\) URLs can be opened/);
    }
    expect(h.launcher.launches).toHaveLength(0);
  });

  it('needs a running app for relative URLs', async () => {
    const h = setup();
    const r = await h.call('browser_open', { url: '/settings' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/relative URL but no app is running: call browser_start_app/);
  });

  it('reports navigation failures', async () => {
    const h = setup({ failOn: new Set(['goto']) });
    const r = await h.call('browser_open', { url: 'http://localhost:1/' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Could not open http:\/\/localhost:1\/: page.goto: net::ERR_CONNECTION_REFUSED/);
  });

  it('relaunches after the browser disconnects', async () => {
    const h = setup();
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    h.launcher.browser.connected = false;
    expect((await h.call('browser_read')).isError).toBe(true);
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    expect(h.launcher.launches).toHaveLength(2);
  });
});

describe('interaction tools', () => {
  async function opened(behaviour: Partial<FakeBehaviour> = {}) {
    const h = setup(behaviour);
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    return h;
  }

  it('browser_click by selector, text and role', async () => {
    const h = await opened();
    expect(textOf(await h.call('browser_click', { selector: '#save' }))).toContain('Clicked selector "#save".');
    expect(textOf(await h.call('browser_click', { text: 'Sign in' }))).toContain('Clicked text "Sign in".');
    expect(textOf(await h.call('browser_click', { role: 'button', name: 'Save' }))).toContain('Clicked button "Save".');
    const clicks = h.launcher.browser.page.calls.filter((c) => c[0] === 'click').map((c) => c[1]);
    expect(clicks).toEqual(['locator(#save)', 'text(Sign in)', 'role(button,Save)']);
  });

  it('browser_click rejects ambiguous targets and reports driver failures', async () => {
    const h = await opened({ failOn: new Set(['click']) });
    let r = await h.call('browser_click', { selector: 'a', text: 'b' });
    expect(textOf(r)).toMatch(/exactly one of "selector", "text", "role" \(\+ "name"\)/);
    r = await h.call('browser_click', { name: 'Save' });
    expect(textOf(r)).toMatch(/exactly one of/);
    r = await h.call('browser_click', { selector: '#missing' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Clicking selector "#missing" failed: locator.click: Timeout 15000ms exceeded\.\n {2}- waiting for/);
  });

  it('browser_type fills, appends and submits', async () => {
    const h = await opened();
    await h.call('browser_type', { label: 'Email', text: 'a@b.c' });
    await h.call('browser_type', { role: 'textbox', name: 'Search', text: 'flow', clear: false, submit: true });
    const r = await h.call('browser_type', { selector: '#x', text: '' });
    expect(textOf(r)).toContain('Typed 0 characters');
    const calls = h.launcher.browser.page.calls.filter((c) => ['fill', 'pressSequentially', 'press'].includes(c[0] as string));
    expect(calls.map((c) => c.slice(0, 3))).toEqual([
      ['fill', 'label(Email)', 'a@b.c'],
      ['pressSequentially', 'role(textbox,Search)', 'flow'],
      ['press', 'role(textbox,Search)', 'Enter'],
      ['fill', 'locator(#x)', ''],
    ]);
    expect(textOf(await h.call('browser_type', { text: 'x' }))).toMatch(/exactly one of "selector", "role" \(\+ "name"\), "label"/);
    expect(textOf(await h.call('browser_type', { text: 'x', selector: 'a', label: 'b' }))).toMatch(/exactly one of/);
    expect(textOf(await h.call('browser_type', { selector: 'a' }))).toMatch(/"text" is required/);
  });

  it('browser_type reports fill failures', async () => {
    const h = await opened({ failOn: new Set(['fill']) });
    const r = await h.call('browser_type', { selector: '#x', text: 'y' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Filling selector "#x" failed/);
  });

  it('browser_press', async () => {
    const h = await opened();
    expect(textOf(await h.call('browser_press', { key: 'Control+K' }))).toContain('Pressed Control+K.');
    const bad = await opened({ failOn: new Set(['keyboard.press']) });
    const r = await bad.call('browser_press', { key: 'Nope' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Pressing Nope failed: Unknown key/);
  });

  it('browser_wait_for selector, text and url', async () => {
    const h = await opened();
    expect(textOf(await h.call('browser_wait_for', { selector: '.toast' }))).toContain('Done waiting for selector ".toast" to be visible.');
    expect(textOf(await h.call('browser_wait_for', { text: 'Saved', timeoutSeconds: 3 }))).toContain('text "Saved"');
    expect(textOf(await h.call('browser_wait_for', { urlIncludes: 'localhost' }))).toContain('URL to include "localhost"');
    const r = await h.call('browser_wait_for', { urlIncludes: '/dashboard', timeoutSeconds: 2 });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Timed out after 2s waiting for URL to include "\/dashboard"[\s\S]*Current URL: http:\/\/localhost:1234\//);
    const waits = h.launcher.browser.page.calls.filter((c) => c[0] === 'waitFor');
    expect(waits[1]![2]).toEqual({ state: 'visible', timeout: 3000 });
  });

  it('browser_read returns visible text, console errors and failed requests', async () => {
    const h = await opened({
      innerText: 'A'.repeat(500),
      consoleErrorsOnGoto: ['boom'],
      failedResponsesOnGoto: [{ url: 'http://localhost:1234/missing.js', status: 404 }],
    });
    h.launcher.browser.page.emit('requestfailed', { method: () => 'POST', url: () => 'http://x/api', failure: () => ({ errorText: 'net::ERR_FAILED' }) });
    h.launcher.browser.page.emit('pageerror', new Error('Uncaught ReferenceError: foo'));
    const r = await h.call('browser_read', { maxChars: 100, selector: 'main' });
    const t = textOf(r);
    expect(r.isError).toBeFalsy();
    expect(t).toContain('--- Visible text of main ---');
    expect(t).toContain(`${'A'.repeat(100)}\n… [truncated 400 characters]`);
    expect(t).toContain('console.error: boom');
    expect(t).toContain('uncaught exception: Uncaught ReferenceError: foo');
    expect(t).toContain('GET http://localhost:1234/missing.js → HTTP 404');
    expect(t).toContain('POST http://x/api failed: net::ERR_FAILED');
    expect(h.launcher.browser.page.calls).toContainEqual(['innerText', 'locator(main)', { timeout: 15000 }]);
  });

  it('browser_read reports read failures', async () => {
    const h = await opened({ failOn: new Set(['innerText']) });
    const r = await h.call('browser_read');
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Reading the page failed/);
  });
});

describe('browser_screenshot', () => {
  it('captures a PNG, saves it, uploads it and returns an image block', async () => {
    const h = setup({ pngSize: 2048 });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    const r = await h.call('browser_screenshot', { label: 'after', group: 'login form', fullPage: true });
    expect(r.isError).toBeFalsy();
    const t = textOf(r);
    expect(t).toMatch(/Screenshot "after" \(group "login form"\) uploaded as artifact art_1; it is attached to the session and the PR\./);
    const image = r.content.find((c) => c.type === 'image');
    expect(image).toMatchObject({ type: 'image', mimeType: 'image/png' });
    expect(Buffer.from((image as { data: string }).data, 'base64').subarray(0, 8)).toEqual(Buffer.from(PNG_MAGIC));

    const saved = (await readdir(h.outputDir)).filter((f) => f.endsWith('-after.png'));
    expect(saved).toHaveLength(1);
    expect(t).toContain(path.join(h.outputDir, saved[0]!));
    expect(h.launcher.browser.page.calls).toContainEqual(['screenshot', { type: 'png', timeout: 30000, fullPage: true }]);

    const call = h.fetchMock.mock.calls[0]!;
    expect(call[0]).toBe('http://flow.test/internal/coding-runtime/artifacts');
    expect((call[1]!.headers as Record<string, string>).Authorization).toBe(`Bearer ${TOKEN}`);
    const form = formOf(h);
    expect(form.get('kind')).toBe('screenshot');
    expect(form.get('label')).toBe('after');
    expect(form.get('group')).toBe('login form');
    expect(form.get('turnId')).toBe('turn_9');
    expect((form.get('file') as File).type).toBe('image/png');
    expect(t).not.toContain(TOKEN);
  });

  it('screenshots a single element by selector', async () => {
    const h = setup();
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    await h.call('browser_screenshot', { label: 'card', selector: '.card' });
    expect(h.launcher.browser.page.calls).toContainEqual(['screenshot', 'locator(.card)', { type: 'png', timeout: 30000 }]);
  });

  it('falls back to JPEG when the PNG is over the limit', async () => {
    env.FLOW_CODING_ARTIFACT_MAX_BYTES = '4096';
    const h = setup({ pngSize: 8000, jpegSize: 3000 });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    const r = await h.call('browser_screenshot', { label: 'before' });
    expect(r.isError).toBeFalsy();
    expect(textOf(r)).toContain('JPEG because the PNG exceeded the size limit');
    expect(r.content.find((c) => c.type === 'image')).toMatchObject({ mimeType: 'image/jpeg' });
    expect((formOf(h).get('file') as File).type).toBe('image/jpeg');
    expect(h.launcher.browser.page.calls).toContainEqual(['screenshot', { type: 'jpeg', quality: 80, timeout: 30000, fullPage: false }]);
  });

  it('fails clearly when even the JPEG is too large', async () => {
    env.FLOW_CODING_ARTIFACT_MAX_BYTES = '1000';
    const h = setup({ pngSize: 8000, jpegSize: 3000 });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    const r = await h.call('browser_screenshot', { label: 'before' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Screenshot is larger than the 1000 bytes limit .*take a smaller viewport or a JPEG/);
    expect(h.fetchMock).not.toHaveBeenCalled();
  });

  it('does not upload when upload is false', async () => {
    const h = setup();
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    const r = await h.call('browser_screenshot', { label: 'debug', upload: false });
    expect(textOf(r)).toContain('captured (not uploaded)');
    expect(r.content.some((c) => c.type === 'image')).toBe(true);
    expect(h.fetchMock).not.toHaveBeenCalled();
  });

  it('still returns the image when the upload fails', async () => {
    const h = setup({}, { fetch: async () => json(429, { error: 'artifact limit reached' }) });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    const r = await h.call('browser_screenshot', { label: 'after' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/captured but not uploaded: Artifact limit reached for this turn/);
    expect(r.content.some((c) => c.type === 'image')).toBe(true);
  });

  it('does not inline huge images', async () => {
    env.FLOW_CODING_ARTIFACT_MAX_BYTES = String(20 * 1024 * 1024);
    const h = setup({ pngSize: 4 * 1024 * 1024 });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    const r = await h.call('browser_screenshot', { label: 'full' });
    expect(r.content.some((c) => c.type === 'image')).toBe(false);
    expect(textOf(r)).toContain('too large to show inline');
  });

  it('reports capture failures', async () => {
    const h = setup({ failOn: new Set(['screenshot']) });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    const r = await h.call('browser_screenshot', { label: 'x' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Taking the screenshot failed: page.screenshot: Target closed/);
  });
});

describe('video recording', () => {
  it('records in a new context, finalizes, uploads and reopens the page', async () => {
    const h = setup({ videoSize: 4000 });
    await h.call('browser_open', { url: 'http://localhost:1234/start' });
    const start = await h.call('browser_record_start', { label: 'checkout flow', group: 'checkout', viewport: { width: 1024, height: 768 } });
    expect(textOf(start)).toContain('Recording "checkout flow" started at 1024x768. Reopened http://localhost:1234/start');
    const recCtx = h.launcher.browser.lastContext;
    expect(recCtx.options.recordVideo).toEqual({ dir: path.join(h.outputDir, 'videos'), size: { width: 1024, height: 768 } });
    expect(recCtx.pages[0]!.url()).toBe('http://localhost:1234/start');

    const again = await h.call('browser_record_start', { label: 'second' });
    expect(again.isError).toBe(true);
    expect(textOf(again)).toMatch(/already in progress/);

    await h.call('browser_click', { text: 'Pay' });
    const stop = await h.call('browser_record_stop');
    expect(stop.isError).toBeFalsy();
    expect(textOf(stop)).toMatch(/Video "checkout flow" \(~\d+s, 4 KiB\) uploaded as artifact art_1/);
    expect(recCtx.closed).toBe(true);
    const form = formOf(h);
    expect(form.get('kind')).toBe('video');
    expect(form.get('group')).toBe('checkout');
    const file = form.get('file') as File;
    expect(file.type).toBe('video/webm');
    expect(file.name).toBe('checkout-flow.webm');
    expect(new Uint8Array(await file.arrayBuffer()).subarray(0, 4)).toEqual(Uint8Array.from(WEBM_MAGIC));

    const after = h.launcher.browser.lastContext;
    expect(after).not.toBe(recCtx);
    expect(after.options.recordVideo).toBeUndefined();
    expect(after.pages[0]!.url()).toBe('http://localhost:1234/start');
  });

  it('can start recording before any page is open and via browser_open record:true', async () => {
    const h = setup();
    const r = await h.call('browser_record_start', { label: 'r' });
    expect(textOf(r)).toContain('No page was open');
    await h.call('browser_record_stop');

    const h2 = setup();
    const open = await h2.call('browser_open', { url: 'http://localhost:1234/', record: true });
    expect(textOf(open)).toContain('Recording "recording" is in progress.');
    expect(h2.launcher.browser.lastContext.options.recordVideo).toBeDefined();
  });

  it('rejects videos over the limit with advice', async () => {
    env.FLOW_CODING_ARTIFACT_MAX_BYTES = '1000';
    const h = setup({ videoSize: 5000 });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    await h.call('browser_record_start', { label: 'long' });
    const r = await h.call('browser_record_stop');
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Video is larger than the 1000 bytes limit .*record a shorter flow or use a smaller viewport\. The file is at/);
    expect(h.fetchMock).not.toHaveBeenCalled();
  });

  it('browser_close finalizes and uploads an in-progress recording', async () => {
    const h = setup();
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    await h.call('browser_record_start', { label: 'flow' });
    const browser = h.launcher.browser;
    const r = await h.call('browser_close');
    expect(r.isError).toBeFalsy();
    expect(textOf(r)).toMatch(/Browser closed\.\nVideo "flow" .* uploaded as artifact art_1/);
    expect(browser.connected).toBe(false);
    expect(browser.contexts).toHaveLength(2); // no reopen after close
    expect(formOf(h).get('kind')).toBe('video');
  });

  it('browser_close reports a failed video upload but still closes', async () => {
    const h = setup({}, { fetch: async () => json(500, {}) });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    await h.call('browser_record_start', { label: 'flow' });
    const r = await h.call('browser_close');
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/Browser closed\.[\s\S]*not uploaded: Uploading the video to Flow failed after 4 attempts: HTTP 500/);
    expect(h.launcher.browser.connected).toBe(false);
  });
});

describe('attach_screenshot', () => {
  it('uploads a PNG from the repo with sniffed content type', async () => {
    const h = setup();
    const dir = path.join(base, 'repo', 'test-results');
    await mkdir(dir);
    await writeFile(path.join(dir, 'home page.png'), bytesWithMagic(PNG_MAGIC, 100));
    const r = await h.call('attach_screenshot', { path: 'test-results/home page.png', label: 'after', group: 'home' });
    expect(r.isError).toBeFalsy();
    expect(textOf(r)).toMatch(/Attached .*home page\.png \(image\/png, 100 bytes\) as screenshot artifact art_1 labelled "after" in group "home"/);
    const form = formOf(h);
    expect(form.get('kind')).toBe('screenshot');
    expect((form.get('file') as File).name).toBe('home-page.png');
    expect(r.content.some((c) => c.type === 'image')).toBe(true);
  });

  it('uploads videos as kind=video and detects JPEG regardless of extension', async () => {
    const h = setup();
    await writeFile(path.join(base, 'home', 'clip.bin'), bytesWithMagic(WEBM_MAGIC, 64));
    await writeFile(path.join(base, 'home', 'shot.png'), bytesWithMagic(JPEG_MAGIC, 64));
    await h.call('attach_screenshot', { path: path.join(base, 'home', 'clip.bin'), label: 'demo' });
    await h.call('attach_screenshot', { path: path.join(base, 'home', 'shot.png'), label: 'x' });
    expect(formOf(h, 0).get('kind')).toBe('video');
    expect((formOf(h, 0).get('file') as File).type).toBe('video/webm');
    expect((formOf(h, 1).get('file') as File).type).toBe('image/jpeg');
    expect((formOf(h, 1).get('file') as File).name).toBe('shot.jpg');
  });

  it('rejects other file types, files outside the roots and oversize files', async () => {
    const h = setup();
    await writeFile(path.join(base, 'repo', 'notes.png'), 'not really a png');
    let r = await h.call('attach_screenshot', { path: 'notes.png', label: 'x' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/is not a PNG, JPEG, WebM or MP4 file/);

    await writeFile(path.join(base, 'token.png'), bytesWithMagic(PNG_MAGIC, 10));
    r = await h.call('attach_screenshot', { path: path.join(base, 'token.png'), label: 'x' });
    expect(textOf(r)).toMatch(/Refusing to attach/);

    r = await h.call('attach_screenshot', { path: 'missing.png', label: 'x' });
    expect(textOf(r)).toMatch(/File not found/);

    env.FLOW_CODING_ARTIFACT_MAX_BYTES = '50';
    await writeFile(path.join(base, 'repo', 'big.png'), bytesWithMagic(PNG_MAGIC, 100));
    r = await h.call('attach_screenshot', { path: 'big.png', label: 'x' });
    expect(textOf(r)).toMatch(/Screenshot is larger than the 50 bytes limit/);
    expect(h.fetchMock).not.toHaveBeenCalled();
  });

  it('surfaces upload errors', async () => {
    const h = setup({}, { fetch: async () => json(413, { error: 'file too large' }) });
    await writeFile(path.join(base, 'repo', 'a.png'), bytesWithMagic(PNG_MAGIC, 10));
    const r = await h.call('attach_screenshot', { path: 'a.png', label: 'x' });
    expect(r.isError).toBe(true);
    expect(textOf(r)).toMatch(/HTTP 413/);
    expect(textOf(r)).not.toContain(TOKEN);
  });
});

describe('browser_start_app + relative browser_open (real process)', () => {
  it('starts the app, resolves relative URLs against it, and stops it', async () => {
    const h = setup();
    await writeFile(
      path.join(base, 'repo', 'package.json'),
      JSON.stringify({
        scripts: { dev: `node -e "require('http').createServer((q,r)=>r.end('ok')).listen(0,'127.0.0.1',function(){console.log('Local: http://127.0.0.1:'+this.address().port+'/')})"` },
      }),
    );
    try {
      const r = await h.call('browser_start_app', { readyTimeoutSeconds: 30 });
      expect(r.isError, textOf(r)).toBeFalsy();
      const url = /running at (http:\/\/127\.0\.0\.1:\d+)/.exec(textOf(r))?.[1];
      expect(url).toBeTruthy();
      expect(textOf(r)).toContain('Command: `npm run dev` (from package.json)');
      const again = await h.call('browser_start_app');
      expect(textOf(again)).toContain(`already running at ${url}`);
      const open = await h.call('browser_open', { url: '/settings?tab=1' });
      expect(textOf(open)).toContain(`Opened ${url}/settings?tab=1`);
    } finally {
      const stop = await h.call('browser_stop_app');
      expect(textOf(stop)).toContain('Stopped the app.');
    }
  }, 40_000);
});

describe('screenshot files', () => {
  it('writes the exact bytes it uploads', async () => {
    const h = setup({ pngSize: 333 });
    await h.call('browser_open', { url: 'http://localhost:1234/' });
    await h.call('browser_screenshot', { label: 'Before: Settings page!' });
    const [file] = await readdir(h.outputDir);
    expect(file).toMatch(/-before-settings-page\.png$/);
    const onDisk = await readFile(path.join(h.outputDir, file!));
    const uploaded = new Uint8Array(await (formOf(h).get('file') as File).arrayBuffer());
    expect(new Uint8Array(onDisk)).toEqual(uploaded);
  });
});
