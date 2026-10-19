import { describe, expect, it, vi } from 'vitest';

import { IMAGE_PLAYWRIGHT_MODULE, chromiumLaunchOptions, loadChromium, playwrightCandidates, proxyFromEnv } from './playwright.js';

const fakeChromium = { launch: async () => ({}) };

describe('playwright loading', () => {
  it('tries the override, then the image path, then the bare specifier', () => {
    expect(playwrightCandidates({ FLOW_PLAYWRIGHT_MODULE: '/x/playwright/index.mjs' })).toEqual([
      '/x/playwright/index.mjs',
      IMAGE_PLAYWRIGHT_MODULE,
      'playwright',
    ]);
    expect(playwrightCandidates({})).toEqual([IMAGE_PLAYWRIGHT_MODULE, 'playwright']);
  });

  it('uses the first candidate that loads, converting absolute paths to file URLs', async () => {
    const importer = vi.fn(async (spec: string) => {
      if (spec.startsWith('file:///x/')) throw new Error('Cannot find module');
      if (spec.startsWith('file:///opt/flow/')) return { default: { chromium: fakeChromium } };
      return { chromium: { launch: () => 'bare' } };
    });
    await expect(loadChromium({ FLOW_PLAYWRIGHT_MODULE: '/x/pw.mjs' }, importer)).resolves.toBe(fakeChromium);
    expect(importer.mock.calls.map((c) => c[0])).toEqual(['file:///x/pw.mjs', `file://${IMAGE_PLAYWRIGHT_MODULE}`]);
  });

  it('falls back to the bare specifier', async () => {
    const importer = vi.fn(async (spec: string) => {
      if (spec === 'playwright') return { chromium: fakeChromium };
      throw new Error('ERR_MODULE_NOT_FOUND');
    });
    await expect(loadChromium({}, importer)).resolves.toBe(fakeChromium);
  });

  it('explains when Playwright is missing', async () => {
    const importer = vi.fn(async (spec: string) => {
      if (spec === 'playwright') return { something: 1 };
      throw new Error('Cannot find module\nstack...');
    });
    await expect(loadChromium({}, importer)).rejects.toThrow(
      /Playwright is not available in this sandbox[\s\S]*index\.mjs: Cannot find module\n- playwright: module has no chromium launcher/,
    );
  });
});

describe('chromiumLaunchOptions', () => {
  it('runs headless without the Chromium sandbox and with --disable-dev-shm-usage', () => {
    const o = chromiumLaunchOptions({});
    expect(o.headless).toBe(true);
    expect(o.chromiumSandbox).toBe(false);
    expect(o.args).toContain('--disable-dev-shm-usage');
    expect(o.proxy).toBeUndefined();
    expect(o.timeout).toBeGreaterThan(0);
  });

  it('passes the egress proxy with credentials split out and loopback bypassed', () => {
    const o = chromiumLaunchOptions({ HTTPS_PROXY: 'http://sbx_1:fcs%2Ftok@proxy.flow:3128', NO_PROXY: 'flow-api,*,.svc' });
    expect(o.proxy).toEqual({
      server: 'http://proxy.flow:3128',
      username: 'sbx_1',
      password: 'fcs/tok',
      bypass: 'localhost,127.0.0.1,[::1],flow-api,.svc',
    });
    expect(JSON.stringify(o.args)).not.toContain('fcs');
  });

  it('falls back to HTTP_PROXY and tolerates scheme-less values', () => {
    expect(proxyFromEnv({ http_proxy: 'proxy:3128' })?.server).toBe('http://proxy:3128');
    expect(proxyFromEnv({ HTTPS_PROXY: '   ' })).toBeUndefined();
  });
});
