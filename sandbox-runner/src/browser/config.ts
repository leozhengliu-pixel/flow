// Environment contract of the browser tools. Track C's runner writes these into the env of the
// stdio MCP server `flow` (see the header of src/mcp/tools/browser.ts for the full list).
import { readFile } from 'node:fs/promises';
import { homedir } from 'node:os';
import path from 'node:path';

import { ToolError } from './util.js';

export const DEFAULT_ARTIFACT_MAX_BYTES = 10 * 1024 * 1024;
export const DEFAULT_OUTPUT_DIR = '/tmp/flow-browser';
export const DEFAULT_REPO_DIR = '/workspace/repo';
export const DEFAULT_VIEWPORT = { width: 1280, height: 720 } as const;

export function homeDir(env: NodeJS.ProcessEnv): string {
  return env.HOME && env.HOME.trim() ? env.HOME : homedir();
}

/** FLOW_BROWSER_ENABLED=0 (or false/off/no) disables the tools even if the bootstrap enabled them. */
export function browserEnabledByEnv(env: NodeJS.ProcessEnv): boolean {
  const v = (env.FLOW_BROWSER_ENABLED ?? '').trim().toLowerCase();
  return !['0', 'false', 'off', 'no'].includes(v);
}

/** FLOW_CODING_ARTIFACT_MAX_BYTES, falling back to the server default of 10 MiB. */
export function artifactMaxBytes(env: NodeJS.ProcessEnv): number {
  const raw = env.FLOW_CODING_ARTIFACT_MAX_BYTES?.trim();
  if (!raw) return DEFAULT_ARTIFACT_MAX_BYTES;
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : DEFAULT_ARTIFACT_MAX_BYTES;
}

export function runtimeUrl(env: NodeJS.ProcessEnv): string {
  const raw = env.FLOW_RUNTIME_URL?.trim();
  if (!raw) throw new ToolError('Cannot upload artifacts: FLOW_RUNTIME_URL is not set in the runner environment.');
  try {
    const u = new URL(raw);
    if (u.protocol !== 'http:' && u.protocol !== 'https:') throw new Error('bad scheme');
  } catch {
    throw new ToolError('Cannot upload artifacts: FLOW_RUNTIME_URL is not a valid http(s) URL.');
  }
  return raw.replace(/\/+$/, '');
}

/** Reads the sandbox token on every call (it may be rotated). Never log the returned value. */
export async function readToken(env: NodeJS.ProcessEnv): Promise<string> {
  const file = env.FLOW_TOKEN_FILE?.trim();
  if (!file) throw new ToolError('Cannot upload artifacts: FLOW_TOKEN_FILE is not set in the runner environment.');
  let token: string;
  try {
    token = (await readFile(file, 'utf8')).trim();
  } catch {
    throw new ToolError('Cannot upload artifacts: the sandbox token file is not readable.');
  }
  if (!token) throw new ToolError('Cannot upload artifacts: the sandbox token file is empty.');
  return token;
}

/** Current turn id: FLOW_TURN_ID, else $HOME/.flow/current-turn, else undefined. */
export async function currentTurnId(env: NodeJS.ProcessEnv): Promise<string | undefined> {
  const fromEnv = env.FLOW_TURN_ID?.trim();
  if (fromEnv) return fromEnv;
  try {
    const fromFile = (await readFile(path.join(homeDir(env), '.flow', 'current-turn'), 'utf8')).trim();
    return fromFile || undefined;
  } catch {
    return undefined;
  }
}
