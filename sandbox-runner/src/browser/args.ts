// Argument validation for tool handlers. Models send loosely typed JSON, so every handler validates
// its input explicitly and reports problems as plain text instead of crashing.
import type { Viewport } from './types.js';
import { ToolError } from './util.js';

export class ArgError extends ToolError {
  constructor(message: string) {
    super(`Invalid arguments: ${message}`);
    this.name = 'ArgError';
  }
}

type Args = Record<string, unknown>;

export function optString(args: Args, key: string, opts: { max?: number; allowEmpty?: boolean } = {}): string | undefined {
  const v = args[key];
  if (v === undefined || v === null) return undefined;
  if (typeof v !== 'string') throw new ArgError(`"${key}" must be a string`);
  const trimmed = v.trim();
  if (!trimmed && !opts.allowEmpty) return undefined;
  if (opts.max !== undefined && trimmed.length > opts.max) {
    throw new ArgError(`"${key}" must be at most ${opts.max} characters`);
  }
  return opts.allowEmpty ? v : trimmed;
}

export function reqString(args: Args, key: string, opts: { max?: number; allowEmpty?: boolean } = {}): string {
  const v = optString(args, key, opts);
  if (v === undefined) throw new ArgError(`"${key}" is required`);
  return v;
}

export function optBool(args: Args, key: string): boolean | undefined {
  const v = args[key];
  if (v === undefined || v === null) return undefined;
  if (typeof v !== 'boolean') throw new ArgError(`"${key}" must be true or false`);
  return v;
}

export function optInt(args: Args, key: string, min: number, max: number): number | undefined {
  const v = args[key];
  if (v === undefined || v === null) return undefined;
  if (typeof v !== 'number' || !Number.isFinite(v) || !Number.isInteger(v)) {
    throw new ArgError(`"${key}" must be an integer`);
  }
  if (v < min || v > max) throw new ArgError(`"${key}" must be between ${min} and ${max}`);
  return v;
}

export function optNumber(args: Args, key: string, min: number, max: number): number | undefined {
  const v = args[key];
  if (v === undefined || v === null) return undefined;
  if (typeof v !== 'number' || !Number.isFinite(v)) throw new ArgError(`"${key}" must be a number`);
  if (v < min || v > max) throw new ArgError(`"${key}" must be between ${min} and ${max}`);
  return v;
}

export function optViewport(args: Args, key = 'viewport'): Viewport | undefined {
  const v = args[key];
  if (v === undefined || v === null) return undefined;
  if (typeof v !== 'object' || Array.isArray(v)) {
    throw new ArgError(`"${key}" must be an object like {"width":1280,"height":720}`);
  }
  const obj = v as Args;
  const width = optInt(obj, 'width', 200, 3840);
  const height = optInt(obj, 'height', 200, 2160);
  if (width === undefined || height === undefined) {
    throw new ArgError(`"${key}" needs both "width" (200-3840) and "height" (200-2160)`);
  }
  return { width, height };
}

/** Validates the artifact label/group pair shared by the capture tools. */
export function labelAndGroup(args: Args): { label: string; group?: string } {
  const label = reqString(args, 'label', { max: 120 });
  const group = optString(args, 'group', { max: 80 });
  return group === undefined ? { label } : { label, group };
}
