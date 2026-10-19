// Runtime loading of Playwright and Chromium launch options.
//
// Playwright is NOT an npm dependency of the runner. install-browser.sh puts it at
// /opt/flow/playwright/node_modules/playwright and the browsers at /opt/ms-playwright
// (PLAYWRIGHT_BROWSERS_PATH). We import it dynamically, trying in order:
//   1. $FLOW_PLAYWRIGHT_MODULE (path or package specifier)
//   2. /opt/flow/playwright/node_modules/playwright/index.mjs
//   3. the bare specifier "playwright"
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import type { BrowserLauncher, LaunchOptions } from './types.js';
import { ToolError, errorMessage } from './util.js';

export const IMAGE_PLAYWRIGHT_MODULE = '/opt/flow/playwright/node_modules/playwright/index.mjs';

export type Importer = (specifier: string) => Promise<unknown>;

const defaultImporter: Importer = (specifier) => import(specifier);

export function playwrightCandidates(env: NodeJS.ProcessEnv): string[] {
  const out: string[] = [];
  const override = env.FLOW_PLAYWRIGHT_MODULE?.trim();
  if (override) out.push(override);
  out.push(IMAGE_PLAYWRIGHT_MODULE, 'playwright');
  return [...new Set(out)];
}

function toSpecifier(candidate: string): string {
  return path.isAbsolute(candidate) ? pathToFileURL(candidate).href : candidate;
}

/** Imports Playwright and returns its chromium launcher; the first candidate that loads wins. */
export async function loadChromium(env: NodeJS.ProcessEnv, importer: Importer = defaultImporter): Promise<BrowserLauncher> {
  const failures: string[] = [];
  for (const candidate of playwrightCandidates(env)) {
    try {
      const mod = (await importer(toSpecifier(candidate))) as {
        chromium?: unknown;
        default?: { chromium?: unknown };
      };
      const chromium = mod.chromium ?? mod.default?.chromium;
      if (chromium && typeof (chromium as { launch?: unknown }).launch === 'function') {
        return chromium as BrowserLauncher;
      }
      failures.push(`${candidate}: module has no chromium launcher`);
    } catch (err) {
      failures.push(`${candidate}: ${errorMessage(err).split('\n')[0]}`);
    }
  }
  throw new ToolError(
    `Playwright is not available in this sandbox, so browser tools cannot run. Tried:\n- ${failures.join('\n- ')}`,
  );
}

/**
 * Chromium launch options for the sandbox Pod.
 *
 * chromiumSandbox: false — the Pod runs as uid 1000 with every capability dropped, no privilege
 * escalation and seccomp RuntimeDefault (design §19). Chromium's own sandbox needs user namespaces
 * or a setuid helper, neither of which is available there, so it cannot start. The Pod (plus the
 * NetworkPolicy and egress proxy) is the security boundary for everything the agent runs, the
 * browser included; disabling Chromium's inner sandbox does not grant the page anything the agent's
 * shell could not already do.
 *
 * --disable-dev-shm-usage — /dev/shm in a Pod is 64 MiB by default; Chromium would crash on larger
 * pages, so it writes shared memory files to /tmp instead.
 *
 * Proxy — sandboxes reach the internet only through the egress proxy (HTTPS_PROXY, which carries
 * `sandboxId:token` credentials). Chromium does not accept credentials in the proxy URL, so they go
 * to Playwright's username/password fields. The locally started app must be reached directly, so
 * loopback hosts (and NO_PROXY entries) are bypassed; Chromium also bypasses loopback implicitly.
 */
export function chromiumLaunchOptions(env: NodeJS.ProcessEnv): LaunchOptions {
  const options: LaunchOptions = {
    headless: true,
    chromiumSandbox: false,
    args: ['--disable-dev-shm-usage', '--no-first-run', '--no-default-browser-check'],
    timeout: 60_000,
  };
  const proxy = proxyFromEnv(env);
  if (proxy) options.proxy = proxy;
  return options;
}

export function proxyFromEnv(env: NodeJS.ProcessEnv): LaunchOptions['proxy'] | undefined {
  const raw = (env.HTTPS_PROXY || env.https_proxy || env.HTTP_PROXY || env.http_proxy || '').trim();
  if (!raw) return undefined;
  let u: URL;
  try {
    u = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `http://${raw}`);
  } catch {
    return undefined;
  }
  const bypass = new Set(['localhost', '127.0.0.1', '[::1]']);
  for (const entry of (env.NO_PROXY || env.no_proxy || '').split(',')) {
    const e = entry.trim();
    if (e && e !== '*') bypass.add(e);
  }
  const proxy: NonNullable<LaunchOptions['proxy']> = {
    server: `${u.protocol}//${u.host}`,
    bypass: [...bypass].join(','),
  };
  if (u.username) proxy.username = decodeURIComponent(u.username);
  if (u.password) proxy.password = decodeURIComponent(u.password);
  return proxy;
}
