// browser_start_app / browser_stop_app: start the project's dev server in the background and wait
// until it serves HTTP.
//
// Command resolution order (per field): tool arguments → FLOW_BROWSER_APP_COMMAND / _URL / _CWD
// (environment variables configured on the Flow environment) → `.flow/browser.json` in the repo
// (`{command,url,cwd,port,readyPattern,env}`) → detection from package.json scripts
// (dev, start, preview, serve) run with the package manager inferred from lockfiles.
import { spawn, type ChildProcess } from 'node:child_process';
import { access, readFile } from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';

import { RingBuffer, ToolError, sleep, stripAnsi, truncate } from './util.js';

export type AppSource = 'arguments' | 'environment' | '.flow/browser.json' | 'package.json';

export interface AppStartInput {
  command?: string;
  url?: string;
  cwd?: string;
  port?: number;
}

export interface AppConfig {
  command: string;
  source: AppSource;
  cwd: string;
  url?: string;
  port?: number;
  readyPattern?: RegExp;
  env: Record<string, string>;
}

interface BrowserJson {
  command?: string;
  url?: string;
  cwd?: string;
  port?: number;
  readyPattern?: string;
  env?: Record<string, string>;
}

export const SCRIPT_ORDER = ['dev', 'start', 'preview', 'serve'] as const;
export type PackageManager = 'pnpm' | 'yarn' | 'bun' | 'npm';

async function exists(p: string): Promise<boolean> {
  try {
    await access(p);
    return true;
  } catch {
    return false;
  }
}

/**
 * Infers the package manager from lockfiles in `dir`, walking up to (and including) `stopAt` so a
 * workspace package inside a monorepo uses the root lockfile.
 */
export async function detectPackageManager(dir: string, stopAt: string): Promise<PackageManager> {
  let current = path.resolve(dir);
  const stop = path.resolve(stopAt);
  for (;;) {
    if (await exists(path.join(current, 'pnpm-lock.yaml'))) return 'pnpm';
    if (await exists(path.join(current, 'yarn.lock'))) return 'yarn';
    if ((await exists(path.join(current, 'bun.lockb'))) || (await exists(path.join(current, 'bun.lock')))) return 'bun';
    if (await exists(path.join(current, 'package-lock.json'))) return 'npm';
    const rel = path.relative(stop, current);
    const parent = path.dirname(current);
    if (rel === '' || rel.startsWith('..') || path.isAbsolute(rel) || parent === current) return 'npm';
    current = parent;
  }
}

/** `<pm> run <script>` for the first of dev/start/preview/serve defined in `dir/package.json`. */
export async function detectPackageScript(dir: string, repoDir: string): Promise<string | undefined> {
  let pkg: unknown;
  try {
    pkg = JSON.parse(await readFile(path.join(dir, 'package.json'), 'utf8'));
  } catch {
    return undefined;
  }
  const scripts = (pkg as { scripts?: unknown }).scripts;
  if (!scripts || typeof scripts !== 'object') return undefined;
  const script = SCRIPT_ORDER.find((name) => typeof (scripts as Record<string, unknown>)[name] === 'string');
  if (!script) return undefined;
  const pm = await detectPackageManager(dir, repoDir);
  return `${pm} run ${script}`;
}

async function readBrowserJson(repoDir: string): Promise<BrowserJson | undefined> {
  const file = path.join(repoDir, '.flow', 'browser.json');
  let raw: string;
  try {
    raw = await readFile(file, 'utf8');
  } catch {
    return undefined;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    throw new ToolError(`.flow/browser.json is not valid JSON: ${(err as Error).message}`);
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new ToolError('.flow/browser.json must contain a JSON object like {"command":"npm run dev","url":"http://127.0.0.1:3000"}.');
  }
  const obj = parsed as Record<string, unknown>;
  const out: BrowserJson = {};
  for (const key of ['command', 'url', 'cwd', 'readyPattern'] as const) {
    const v = obj[key];
    if (v === undefined) continue;
    if (typeof v !== 'string') throw new ToolError(`.flow/browser.json: "${key}" must be a string.`);
    if (v.trim()) out[key] = v.trim();
  }
  if (obj.port !== undefined) {
    if (typeof obj.port !== 'number' || !Number.isInteger(obj.port) || obj.port < 1 || obj.port > 65535) {
      throw new ToolError('.flow/browser.json: "port" must be an integer between 1 and 65535.');
    }
    out.port = obj.port;
  }
  if (obj.env !== undefined) {
    if (!obj.env || typeof obj.env !== 'object' || Array.isArray(obj.env)) {
      throw new ToolError('.flow/browser.json: "env" must be an object of string values.');
    }
    const env: Record<string, string> = {};
    for (const [k, v] of Object.entries(obj.env as Record<string, unknown>)) {
      if (typeof v !== 'string' && typeof v !== 'number' && typeof v !== 'boolean') {
        throw new ToolError(`.flow/browser.json: env "${k}" must be a string.`);
      }
      env[k] = String(v);
    }
    out.env = env;
  }
  return out;
}

function nonEmpty(v: string | undefined): string | undefined {
  return v && v.trim() ? v.trim() : undefined;
}

export async function resolveAppConfig(input: AppStartInput, env: NodeJS.ProcessEnv, repoDir: string): Promise<AppConfig> {
  const file = await readBrowserJson(repoDir);
  const envCommand = nonEmpty(env.FLOW_BROWSER_APP_COMMAND);

  const cwdSpec = input.cwd ?? nonEmpty(env.FLOW_BROWSER_APP_CWD) ?? file?.cwd;
  const cwd = cwdSpec ? path.resolve(repoDir, cwdSpec) : repoDir;

  let command: string | undefined;
  let source: AppSource;
  if (input.command) {
    command = input.command;
    source = 'arguments';
  } else if (envCommand) {
    command = envCommand;
    source = 'environment';
  } else if (file?.command) {
    command = file.command;
    source = '.flow/browser.json';
  } else {
    command = await detectPackageScript(cwd, repoDir);
    source = 'package.json';
  }
  if (!command) {
    throw new ToolError(
      `Could not determine how to start the app: no "command" argument, no FLOW_BROWSER_APP_COMMAND, no .flow/browser.json, and ${path.join(
        cwd,
        'package.json',
      )} has no dev/start/preview/serve script. Pass "command" (for example "npm run dev") and, if needed, "url".`,
    );
  }

  let readyPattern: RegExp | undefined;
  if (file?.readyPattern) {
    try {
      readyPattern = new RegExp(file.readyPattern);
    } catch {
      throw new ToolError(`.flow/browser.json: "readyPattern" is not a valid regular expression.`);
    }
  }

  const url = input.url ?? nonEmpty(env.FLOW_BROWSER_APP_URL) ?? file?.url;
  const port = input.port ?? file?.port;
  const config: AppConfig = { command, source, cwd, env: file?.env ?? {} };
  if (url) config.url = normalizeHttpUrl(url, 'url');
  if (port !== undefined) config.port = port;
  if (readyPattern) config.readyPattern = readyPattern;
  return config;
}

function normalizeHttpUrl(raw: string, what: string): string {
  let u: URL;
  try {
    u = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `http://${raw}`);
  } catch {
    throw new ToolError(`"${what}" is not a valid URL: ${raw}`);
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') throw new ToolError(`"${what}" must be an http(s) URL.`);
  return stripRootSlash(u);
}

function stripRootSlash(u: URL): string {
  const s = u.toString();
  return u.pathname === '/' && !u.search && !u.hash ? s.replace(/\/$/, '') : s;
}

const LOCAL_URL_RE = /\bhttps?:\/\/(?:localhost|127(?:\.\d{1,3}){3}|0\.0\.0\.0|\[::1?\])(?::\d{1,5})?(?:\/[^\s'"`<>)\]]*)?/gi;

/**
 * Extracts a loopback URL a dev server printed, e.g. "  ➜  Local:   http://localhost:5173/" or
 * "ready - started server on 0.0.0.0:3000, url: http://localhost:3000". Non-loopback URLs (docs
 * links, "Network:" addresses) are ignored. 0.0.0.0 and [::] become 127.0.0.1.
 */
export function parseLocalUrl(line: string): string | undefined {
  const clean = stripAnsi(line);
  // npm/pnpm/yarn echo the script ("> node server.js --host http://127.0.0.1") before running it;
  // a URL in that echo is configuration, not the address the server ended up on.
  if (/^\s*> /.test(clean)) return undefined;
  for (const match of clean.matchAll(LOCAL_URL_RE)) {
    const candidate = match[0].replace(/[.,;:!?]+$/, '');
    try {
      const u = new URL(candidate);
      if (u.hostname === '0.0.0.0' || u.hostname === '[::]') u.hostname = '127.0.0.1';
      return stripRootSlash(u);
    } catch {
      // keep scanning
    }
  }
  return undefined;
}

function hasExplicitPort(url: string): boolean {
  try {
    return new URL(url).port !== '';
  } catch {
    return false;
  }
}

export async function isPortFree(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const srv = net.createServer();
    srv.once('error', () => resolve(false));
    srv.listen({ port, exclusive: true }, () => srv.close(() => resolve(true)));
  });
}

export async function ephemeralPort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen({ port: 0, host: '127.0.0.1' }, () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(() => (port ? resolve(port) : reject(new Error('could not allocate a port'))));
    });
  });
}

/** Line splitter that keeps the last N lines of a stream pair. */
class LogCollector {
  readonly lines = new RingBuffer<string>(200);
  private partial: Record<'out' | 'err', string> = { out: '', err: '' };

  constructor(private readonly onLine: (line: string) => void) {}

  feed(stream: 'out' | 'err', chunk: Buffer | string): void {
    const data = this.partial[stream] + chunk.toString();
    const parts = data.split(/\r?\n|\r/);
    this.partial[stream] = parts.pop() ?? '';
    if (this.partial[stream].length > 4000) {
      parts.push(this.partial[stream]);
      this.partial[stream] = '';
    }
    for (const part of parts) this.add(part);
  }

  flush(): void {
    for (const stream of ['out', 'err'] as const) {
      if (this.partial[stream]) this.add(this.partial[stream]);
      this.partial[stream] = '';
    }
  }

  private add(raw: string): void {
    const line = stripAnsi(raw).slice(0, 2000);
    if (!line.trim()) return;
    this.lines.push(line);
    this.onLine(line);
  }

  tail(n: number, maxChars = 6000): string {
    const t = this.lines.tail(n).join('\n');
    return t.length > maxChars ? `…${t.slice(t.length - maxChars)}` : t;
  }
}

interface RunningApp {
  child: ChildProcess;
  pid: number;
  config: AppConfig;
  port: number;
  defaultUrl: string;
  detectedUrl?: string;
  readyUrl?: string;
  patternMatched: boolean;
  exit?: { code: number | null; signal: NodeJS.Signals | null };
  exited: Promise<void>;
  logs: LogCollector;
  exitHook: () => void;
}

export interface AppStartResult {
  url: string;
  alreadyRunning: boolean;
  command: string;
  source: AppSource;
  cwd: string;
  logTail: string;
}

export interface AppManagerOptions {
  env: NodeJS.ProcessEnv;
  repoDir: string;
  defaultPort?: number;
  fetch?: typeof fetch;
  pollIntervalMs?: number;
}

export class AppManager {
  private app: RunningApp | undefined;
  private readonly env: NodeJS.ProcessEnv;
  private readonly repoDir: string;
  private readonly defaultPort: number;
  private readonly fetchImpl: typeof fetch;
  private readonly pollIntervalMs: number;

  constructor(opts: AppManagerOptions) {
    this.env = opts.env;
    this.repoDir = opts.repoDir;
    this.defaultPort = opts.defaultPort ?? 3000;
    this.fetchImpl = opts.fetch ?? globalThis.fetch.bind(globalThis);
    this.pollIntervalMs = opts.pollIntervalMs ?? 250;
  }

  /** URL of the running, ready app (used to resolve relative browser_open paths). */
  get url(): string | undefined {
    return this.app && !this.app.exit ? this.app.readyUrl : undefined;
  }

  get running(): boolean {
    return !!this.app && !this.app.exit;
  }

  async start(input: AppStartInput, readyTimeoutMs: number): Promise<AppStartResult> {
    if (this.app?.exit) this.app = undefined;
    if (this.app) {
      const app = this.app;
      const url = app.readyUrl ?? app.config.url ?? app.detectedUrl ?? app.defaultUrl;
      if (app.readyUrl && (await this.healthy(app.readyUrl))) {
        return this.result(app, app.readyUrl, true);
      }
      // Still starting (a previous call timed out) or not responding: keep waiting.
      await this.waitReady(app, readyTimeoutMs);
      return this.result(app, app.readyUrl ?? url, true);
    }

    const config = await resolveAppConfig(input, this.env, this.repoDir);
    if (!(await exists(config.cwd))) throw new ToolError(`App directory does not exist: ${config.cwd}`);
    const port = await this.choosePort(config);
    const defaultUrl = config.url ?? `http://127.0.0.1:${port}`;

    const childEnv: Record<string, string> = {};
    for (const [k, v] of Object.entries(this.env)) if (v !== undefined) childEnv[k] = v;
    Object.assign(childEnv, { PORT: String(port), HOST: '127.0.0.1', BROWSER: 'none', CI: '1' }, config.env);

    const child = spawn('/bin/sh', ['-c', config.command], {
      cwd: config.cwd,
      env: childEnv,
      detached: true, // own process group so stop() can signal the whole tree
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    if (child.pid === undefined) {
      // spawn reports failures asynchronously via 'error'.
      const err = await new Promise<Error>((resolve) => child.once('error', resolve));
      throw new ToolError(`Could not start the app command: ${err.message}`);
    }
    const pid = child.pid;

    const app: RunningApp = {
      child,
      pid,
      config,
      port,
      defaultUrl,
      patternMatched: false,
      exited: Promise.resolve(),
      logs: new LogCollector((line) => {
        // A URL with an explicit port is more specific than one without (some servers print the
        // bare host before the final address), so it replaces an earlier port-less match.
        if (!app.detectedUrl || !hasExplicitPort(app.detectedUrl)) {
          const found = parseLocalUrl(line);
          if (found && (!app.detectedUrl || hasExplicitPort(found))) app.detectedUrl = found;
        }
        if (config.readyPattern?.test(line)) app.patternMatched = true;
      }),
      exitHook: () => killGroup(pid, 'SIGKILL'),
    };
    app.exited = new Promise((resolve) => {
      child.once('exit', (code, signal) => {
        app.logs.flush();
        app.exit = { code, signal };
        resolve();
      });
    });
    child.once('error', (err) => {
      app.logs.lines.push(`[flow] failed to run command: ${err.message}`);
    });
    child.stdout?.on('data', (c: Buffer) => app.logs.feed('out', c));
    child.stderr?.on('data', (c: Buffer) => app.logs.feed('err', c));
    // Never leave a dev server orphaned when the MCP server process exits.
    process.once('exit', app.exitHook);
    this.app = app;

    try {
      await this.waitReady(app, readyTimeoutMs);
    } catch (err) {
      if (app.exit) {
        // Clean up anything the shell left behind in its process group.
        killGroup(pid, 'SIGKILL');
        process.removeListener('exit', app.exitHook);
        this.app = undefined;
      }
      throw err;
    }
    return this.result(app, app.readyUrl ?? defaultUrl, false);
  }

  async stop(): Promise<{ stopped: boolean; logTail: string }> {
    const app = this.app;
    if (!app) return { stopped: false, logTail: '' };
    this.app = undefined;
    process.removeListener('exit', app.exitHook);
    if (!app.exit) {
      killGroup(app.pid, 'SIGTERM');
      const exited = await Promise.race([app.exited.then(() => true), sleep(5000).then(() => false)]);
      if (!exited) {
        killGroup(app.pid, 'SIGKILL');
        await Promise.race([app.exited, sleep(2000)]);
      }
    }
    // The shell may have exited while children of the group linger.
    killGroup(app.pid, 'SIGKILL');
    return { stopped: true, logTail: app.logs.tail(40) };
  }

  private async choosePort(config: AppConfig): Promise<number> {
    if (config.port !== undefined) return config.port;
    const envPort = Number(config.env.PORT);
    if (Number.isInteger(envPort) && envPort > 0) return envPort;
    if (config.url) {
      const u = new URL(config.url);
      if (u.port) return Number(u.port);
    }
    if (await isPortFree(this.defaultPort)) return this.defaultPort;
    return ephemeralPort();
  }

  private async waitReady(app: RunningApp, timeoutMs: number): Promise<void> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      if (app.exit) {
        const how = app.exit.signal ? `was killed by ${app.exit.signal}` : `exited with code ${app.exit.code}`;
        throw new ToolError(
          `The app command \`${app.config.command}\` ${how} before it became ready.\n\nLast output:\n${app.logs.tail(80) || '(no output)'}`,
        );
      }
      const candidates = unique([app.config.url, app.detectedUrl, app.defaultUrl]);
      for (const candidate of candidates) {
        if (await this.healthy(candidate)) {
          app.readyUrl = candidate;
          return;
        }
      }
      if (app.patternMatched && candidates[0]) {
        app.readyUrl = candidates[0];
        return;
      }
      if (Date.now() >= deadline) {
        throw new ToolError(
          `The app did not respond at ${candidates.join(' or ')} within ${Math.round(timeoutMs / 1000)}s. It is still running in the background: call browser_start_app again to keep waiting (optionally with a larger readyTimeoutSeconds), pass "url" if it serves on a different address, or call browser_stop_app.\n\nLast output:\n${
            app.logs.tail(80) || '(no output)'
          }`,
        );
      }
      await Promise.race([sleep(this.pollIntervalMs), app.exited]);
    }
  }

  private async healthy(url: string): Promise<boolean> {
    try {
      const res = await this.fetchImpl(url, { method: 'GET', redirect: 'manual', signal: AbortSignal.timeout(2000) });
      await res.body?.cancel().catch(() => undefined);
      return true;
    } catch {
      return false;
    }
  }

  private result(app: RunningApp, url: string, alreadyRunning: boolean): AppStartResult {
    return {
      url,
      alreadyRunning,
      command: app.config.command,
      source: app.config.source,
      cwd: app.config.cwd,
      logTail: truncate(app.logs.tail(40, 4000), 4000),
    };
  }
}

function unique(values: Array<string | undefined>): string[] {
  return [...new Set(values.filter((v): v is string => !!v))];
}

function killGroup(pid: number, signal: NodeJS.Signals): void {
  try {
    process.kill(-pid, signal);
  } catch {
    // ESRCH: the group is already gone.
  }
}
