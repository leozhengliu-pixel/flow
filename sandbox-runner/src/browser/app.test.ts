import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import net from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { AppManager, detectPackageManager, detectPackageScript, isPortFree, parseLocalUrl, resolveAppConfig } from './app.js';

let repo: string;

beforeEach(async () => {
  repo = await mkdtemp(path.join(tmpdir(), 'flow-app-'));
});

afterEach(async () => {
  await rm(repo, { recursive: true, force: true });
});

async function pkg(dir: string, scripts: Record<string, string>): Promise<void> {
  await mkdir(dir, { recursive: true });
  await writeFile(path.join(dir, 'package.json'), JSON.stringify({ name: 'x', scripts }));
}

describe('package manager and script detection', () => {
  it.each([
    ['pnpm-lock.yaml', 'pnpm'],
    ['yarn.lock', 'yarn'],
    ['bun.lockb', 'bun'],
    ['bun.lock', 'bun'],
    ['package-lock.json', 'npm'],
  ])('%s → %s', async (lockfile, pm) => {
    await writeFile(path.join(repo, lockfile), '');
    await expect(detectPackageManager(repo, repo)).resolves.toBe(pm);
  });

  it('defaults to npm and walks up to the repo root for monorepos', async () => {
    const app = path.join(repo, 'apps', 'web');
    await mkdir(app, { recursive: true });
    await expect(detectPackageManager(app, repo)).resolves.toBe('npm');
    await writeFile(path.join(repo, 'pnpm-lock.yaml'), '');
    await expect(detectPackageManager(app, repo)).resolves.toBe('pnpm');
  });

  it('does not look above the repo root', async () => {
    const inner = path.join(repo, 'inner');
    await mkdir(inner);
    await writeFile(path.join(repo, 'yarn.lock'), '');
    await expect(detectPackageManager(inner, inner)).resolves.toBe('npm');
  });

  it('prefers dev, then start, preview, serve', async () => {
    await pkg(repo, { build: 'vite build', serve: 's', preview: 'p', start: 'st' });
    await writeFile(path.join(repo, 'yarn.lock'), '');
    await expect(detectPackageScript(repo, repo)).resolves.toBe('yarn run start');
    await pkg(repo, { dev: 'vite', start: 'node .' });
    await expect(detectPackageScript(repo, repo)).resolves.toBe('yarn run dev');
    await pkg(repo, { serve: 'x' });
    await expect(detectPackageScript(repo, repo)).resolves.toBe('yarn run serve');
  });

  it('returns undefined without package.json or matching scripts', async () => {
    await expect(detectPackageScript(repo, repo)).resolves.toBeUndefined();
    await pkg(repo, { build: 'tsc', test: 'vitest' });
    await expect(detectPackageScript(repo, repo)).resolves.toBeUndefined();
    await writeFile(path.join(repo, 'package.json'), '{not json');
    await expect(detectPackageScript(repo, repo)).resolves.toBeUndefined();
  });
});

describe('resolveAppConfig', () => {
  it('uses explicit arguments first', async () => {
    await pkg(repo, { dev: 'vite' });
    const c = await resolveAppConfig({ command: 'make run', url: 'localhost:8000', cwd: 'sub', port: 8000 }, { FLOW_BROWSER_APP_COMMAND: 'x' }, repo);
    expect(c).toMatchObject({ command: 'make run', source: 'arguments', url: 'http://localhost:8000', cwd: path.join(repo, 'sub'), port: 8000 });
  });

  it('then the environment variables', async () => {
    await mkdir(path.join(repo, '.flow'));
    await writeFile(path.join(repo, '.flow', 'browser.json'), JSON.stringify({ command: 'from-file', url: 'http://127.0.0.1:1234' }));
    const c = await resolveAppConfig(
      {},
      { FLOW_BROWSER_APP_COMMAND: 'pnpm dev', FLOW_BROWSER_APP_URL: 'http://127.0.0.1:5173/', FLOW_BROWSER_APP_CWD: 'web' },
      repo,
    );
    expect(c).toMatchObject({ command: 'pnpm dev', source: 'environment', url: 'http://127.0.0.1:5173', cwd: path.join(repo, 'web') });
  });

  it('then .flow/browser.json', async () => {
    await pkg(repo, { dev: 'vite' });
    await mkdir(path.join(repo, '.flow'));
    await writeFile(
      path.join(repo, '.flow', 'browser.json'),
      JSON.stringify({ command: 'npm run storybook', url: 'http://127.0.0.1:6006', cwd: 'ui', port: 6006, readyPattern: 'started', env: { NODE_ENV: 'development', N: 1 } }),
    );
    const c = await resolveAppConfig({}, {}, repo);
    expect(c).toMatchObject({ command: 'npm run storybook', source: '.flow/browser.json', port: 6006, cwd: path.join(repo, 'ui') });
    expect(c.env).toEqual({ NODE_ENV: 'development', N: '1' });
    expect(c.readyPattern?.test('Storybook started')).toBe(true);
  });

  it('then package.json scripts in the resolved cwd', async () => {
    await pkg(path.join(repo, 'web'), { start: 'next start' });
    await writeFile(path.join(repo, 'pnpm-lock.yaml'), '');
    const c = await resolveAppConfig({ cwd: 'web' }, {}, repo);
    expect(c).toMatchObject({ command: 'pnpm run start', source: 'package.json', cwd: path.join(repo, 'web') });
    expect(c.url).toBeUndefined();
  });

  it('explains when nothing can be detected', async () => {
    await expect(resolveAppConfig({}, {}, repo)).rejects.toThrow(/Could not determine how to start the app/);
  });

  it('validates .flow/browser.json', async () => {
    await mkdir(path.join(repo, '.flow'));
    const file = path.join(repo, '.flow', 'browser.json');
    await writeFile(file, '{oops');
    await expect(resolveAppConfig({}, {}, repo)).rejects.toThrow(/not valid JSON/);
    await writeFile(file, '[]');
    await expect(resolveAppConfig({}, {}, repo)).rejects.toThrow(/must contain a JSON object/);
    await writeFile(file, JSON.stringify({ command: 1 }));
    await expect(resolveAppConfig({}, {}, repo)).rejects.toThrow(/"command" must be a string/);
    await writeFile(file, JSON.stringify({ command: 'x', port: 70000 }));
    await expect(resolveAppConfig({}, {}, repo)).rejects.toThrow(/"port" must be an integer/);
    await writeFile(file, JSON.stringify({ command: 'x', readyPattern: '(' }));
    await expect(resolveAppConfig({}, {}, repo)).rejects.toThrow(/readyPattern/);
    await writeFile(file, JSON.stringify({ command: 'x', env: { A: {} } }));
    await expect(resolveAppConfig({}, {}, repo)).rejects.toThrow(/env "A" must be a string/);
  });

  it('rejects non-http app URLs', async () => {
    await expect(resolveAppConfig({ command: 'x', url: 'file:///etc/passwd' }, {}, repo)).rejects.toThrow(/http\(s\) URL/);
  });
});

describe('parseLocalUrl', () => {
  it.each([
    ['  \u001b[32m➜\u001b[39m  \u001b[1mLocal\u001b[22m:   \u001b[36mhttp://localhost:\u001b[1m5173\u001b[22m/\u001b[39m', 'http://localhost:5173'],
    ['  - Local:        http://127.0.0.1:3000/', 'http://127.0.0.1:3000'],
    ['ready - started server on 0.0.0.0:3000, url: http://0.0.0.0:3000', 'http://127.0.0.1:3000'],
    ['Listening on http://[::1]:4000/app.', 'http://[::1]:4000/app'],
    ['Server running at http://localhost:8080/admin?x=1', 'http://localhost:8080/admin?x=1'],
  ])('%j → %s', (line, url) => {
    expect(parseLocalUrl(line)).toBe(url);
  });

  it('ignores non-loopback URLs', () => {
    expect(parseLocalUrl('  ➜  Network: http://10.0.0.5:5173/')).toBeUndefined();
    expect(parseLocalUrl('See https://vitejs.dev/config for help')).toBeUndefined();
    expect(parseLocalUrl('compiled successfully')).toBeUndefined();
    expect(parseLocalUrl("> node -e \"listen(0,'127.0.0.1')\" http://127.0.0.1")).toBeUndefined();
  });
});

const SERVER = (listen: string, log: string) =>
  `exec node -e "const s=require('http').createServer((q,r)=>r.end('ok'));s.listen(${listen},'127.0.0.1',()=>console.log(${log}))"`;

function pidAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

describe('AppManager (real processes)', () => {
  let manager: AppManager | undefined;

  afterEach(async () => {
    await manager?.stop();
    manager = undefined;
  });

  it('starts a server with PORT, is idempotent, and stops the process group', async () => {
    manager = new AppManager({ env: process.env, repoDir: repo, defaultPort: 5185 });
    const r = await manager.start({ command: SERVER('+process.env.PORT', "'listening on '+process.env.PORT+' host '+process.env.HOST") }, 20_000);
    expect(r).toMatchObject({ url: 'http://127.0.0.1:5185', alreadyRunning: false, source: 'arguments', cwd: repo });
    expect(r.logTail).toContain('listening on 5185 host 127.0.0.1');
    expect(manager.url).toBe('http://127.0.0.1:5185');
    expect(await (await fetch('http://127.0.0.1:5185')).text()).toBe('ok');

    const again = await manager.start({ command: 'should-not-run' }, 5_000);
    expect(again).toMatchObject({ url: 'http://127.0.0.1:5185', alreadyRunning: true });

    const stopped = await manager.stop();
    expect(stopped.stopped).toBe(true);
    expect(manager.url).toBeUndefined();
    expect(await isPortFree(5185)).toBe(true);
    await expect(manager.stop()).resolves.toEqual({ stopped: false, logTail: '' });
  }, 30_000);

  it('detects the URL a dev server prints on stdout', async () => {
    manager = new AppManager({ env: process.env, repoDir: repo, defaultPort: 5186 });
    const r = await manager.start({ command: SERVER('0', "'  Local:   http://localhost:'+s.address().port+'/'") }, 20_000);
    expect(r.url).toMatch(/^http:\/\/localhost:\d+$/);
    expect(r.url).not.toContain(':5186');
  }, 30_000);

  it('falls back to an ephemeral port when the default is busy', async () => {
    const blocker = net.createServer();
    await new Promise<void>((resolve) => blocker.listen(5187, resolve));
    try {
      manager = new AppManager({ env: process.env, repoDir: repo, defaultPort: 5187 });
      const r = await manager.start({ command: SERVER('+process.env.PORT', "'port '+process.env.PORT") }, 20_000);
      expect(r.url).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/);
      expect(r.url).not.toBe('http://127.0.0.1:5187');
    } finally {
      await new Promise((resolve) => blocker.close(resolve));
    }
  }, 30_000);

  it('reports an early exit with the log tail', async () => {
    manager = new AppManager({ env: process.env, repoDir: repo, defaultPort: 5188 });
    await expect(manager.start({ command: 'echo "Error: Cannot find module vite" >&2; exit 3' }, 20_000)).rejects.toThrow(
      /exited with code 3 before it became ready[\s\S]*Cannot find module vite/,
    );
    expect(manager.running).toBe(false);
  });

  it('times out but keeps the process for a later call, and stop kills the whole group', async () => {
    manager = new AppManager({ env: process.env, repoDir: repo, defaultPort: 5189, pollIntervalMs: 100 });
    await writeFile(path.join(repo, 'pidfile'), '');
    // The shell forks a child (sleep) so this checks that the group, not just the shell, is killed.
    const err = await manager.start({ command: `sleep 60 & echo $! > pidfile; echo waiting; wait` }, 1_000).catch((e: Error) => e);
    expect((err as Error).message).toMatch(/did not respond at http:\/\/127\.0\.0\.1:5189 within 1s[\s\S]*still running[\s\S]*waiting/);
    expect(manager.running).toBe(true);
    const { readFile } = await import('node:fs/promises');
    const sleepPid = Number((await readFile(path.join(repo, 'pidfile'), 'utf8')).trim());
    expect(pidAlive(sleepPid)).toBe(true);
    await expect(manager.start({}, 1_000)).rejects.toThrow(/did not respond/);
    await manager.stop();
    await new Promise((r) => setTimeout(r, 200));
    expect(pidAlive(sleepPid)).toBe(false);
  }, 20_000);

  it('passes BROWSER=none, CI=1 and env from .flow/browser.json', async () => {
    await mkdir(path.join(repo, '.flow'));
    await writeFile(
      path.join(repo, '.flow', 'browser.json'),
      JSON.stringify({ command: `echo "B=$BROWSER CI=$CI X=$FLOW_X"; ${SERVER('+process.env.PORT', "'up'")}`, env: { FLOW_X: 'from-file' } }),
    );
    manager = new AppManager({ env: process.env, repoDir: repo, defaultPort: 5184 });
    const r = await manager.start({}, 20_000);
    expect(r.source).toBe('.flow/browser.json');
    expect(r.logTail).toContain('B=none CI=1 X=from-file');
  }, 30_000);

  it('rejects a missing cwd', async () => {
    manager = new AppManager({ env: process.env, repoDir: repo, defaultPort: 5183 });
    await expect(manager.start({ command: 'true', cwd: 'nope' }, 1000)).rejects.toThrow(/App directory does not exist/);
  });
});
