// File helpers for attach_screenshot: path restriction and content sniffing.
import { open, realpath, stat } from 'node:fs/promises';
import path from 'node:path';

import type { ArtifactKind, ArtifactMime } from './upload.js';
import { ToolError } from './util.js';

export interface SniffResult {
  contentType: ArtifactMime;
  kind: ArtifactKind;
  extension: 'png' | 'jpg' | 'webm' | 'mp4';
}

/** Identifies PNG, JPEG, WebM and MP4 by magic bytes; anything else returns undefined. */
export function sniffContentType(head: Uint8Array): SniffResult | undefined {
  const b = head;
  if (b.length >= 8 && b[0] === 0x89 && b[1] === 0x50 && b[2] === 0x4e && b[3] === 0x47 && b[4] === 0x0d && b[5] === 0x0a && b[6] === 0x1a && b[7] === 0x0a) {
    return { contentType: 'image/png', kind: 'screenshot', extension: 'png' };
  }
  if (b.length >= 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) {
    return { contentType: 'image/jpeg', kind: 'screenshot', extension: 'jpg' };
  }
  if (b.length >= 4 && b[0] === 0x1a && b[1] === 0x45 && b[2] === 0xdf && b[3] === 0xa3) {
    return { contentType: 'video/webm', kind: 'video', extension: 'webm' };
  }
  // ISO BMFF: [4-byte box size]['ftyp'] at offset 4.
  if (b.length >= 8 && b[4] === 0x66 && b[5] === 0x74 && b[6] === 0x79 && b[7] === 0x70) {
    return { contentType: 'video/mp4', kind: 'video', extension: 'mp4' };
  }
  return undefined;
}

function isWithin(root: string, target: string): boolean {
  const rel = path.relative(root, target);
  return rel === '' || (!rel.startsWith('..') && !path.isAbsolute(rel));
}

/**
 * Resolves `requested` (absolute, or relative to `baseDir`) through symlinks and checks the real
 * path lies under one of `allowedRoots` (which are resolved too). Returns the real path.
 */
export async function resolveAllowedFile(requested: string, baseDir: string, allowedRoots: string[]): Promise<string> {
  if (requested.includes('\0')) throw new ToolError('Invalid path.');
  const absolute = path.resolve(baseDir, requested);
  let real: string;
  try {
    real = await realpath(absolute);
  } catch {
    throw new ToolError(`File not found: ${absolute}`);
  }
  const roots: string[] = [];
  for (const root of allowedRoots) {
    try {
      roots.push(await realpath(root));
    } catch {
      // A root that does not exist cannot contain the file.
    }
  }
  if (!roots.some((root) => isWithin(root, real))) {
    throw new ToolError(`Refusing to attach ${absolute}: only files under ${allowedRoots.join(', ')} can be attached.`);
  }
  const st = await stat(real);
  if (!st.isFile()) throw new ToolError(`Not a regular file: ${absolute}`);
  return real;
}

/** Reads the first `n` bytes of a file. */
export async function readHead(file: string, n = 16): Promise<Uint8Array> {
  const handle = await open(file, 'r');
  try {
    const buf = new Uint8Array(n);
    const { bytesRead } = await handle.read(buf, 0, n, 0);
    return buf.subarray(0, bytesRead);
  } finally {
    await handle.close();
  }
}
