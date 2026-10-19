import { mkdir, mkdtemp, realpath, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { readHead, resolveAllowedFile, sniffContentType } from './files.js';

describe('sniffContentType', () => {
  it('recognises PNG, JPEG, WebM and MP4', () => {
    expect(sniffContentType(Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0]))?.contentType).toBe('image/png');
    expect(sniffContentType(Uint8Array.from([0xff, 0xd8, 0xff, 0xdb]))?.contentType).toBe('image/jpeg');
    expect(sniffContentType(Uint8Array.from([0x1a, 0x45, 0xdf, 0xa3, 1]))).toMatchObject({ contentType: 'video/webm', kind: 'video' });
    expect(sniffContentType(Uint8Array.from([0, 0, 0, 0x20, 0x66, 0x74, 0x79, 0x70, 0x69, 0x73, 0x6f, 0x6d]))).toMatchObject({
      contentType: 'video/mp4',
      extension: 'mp4',
    });
  });

  it('rejects other content, including truncated magic', () => {
    expect(sniffContentType(new TextEncoder().encode('GIF89a...'))).toBeUndefined();
    expect(sniffContentType(new TextEncoder().encode('<svg xmlns='))).toBeUndefined();
    expect(sniffContentType(Uint8Array.from([0x89, 0x50, 0x4e, 0x47]))).toBeUndefined();
    expect(sniffContentType(new Uint8Array())).toBeUndefined();
  });
});

describe('resolveAllowedFile', () => {
  let base: string;
  let allowed: string;
  let outside: string;

  beforeEach(async () => {
    base = await realpath(await mkdtemp(path.join(tmpdir(), 'flow-files-')));
    allowed = path.join(base, 'allowed');
    outside = path.join(base, 'outside');
    await mkdir(allowed);
    await mkdir(outside);
    await writeFile(path.join(allowed, 'shot.png'), 'x');
    await writeFile(path.join(outside, 'secret.png'), 'x');
  });

  afterEach(async () => {
    await rm(base, { recursive: true, force: true });
  });

  it('accepts files under an allowed root, absolute or relative', async () => {
    await expect(resolveAllowedFile(path.join(allowed, 'shot.png'), '/', [allowed])).resolves.toBe(path.join(allowed, 'shot.png'));
    await expect(resolveAllowedFile('shot.png', allowed, [allowed])).resolves.toBe(path.join(allowed, 'shot.png'));
  });

  it('rejects files outside the roots, including via .. and symlinks', async () => {
    await expect(resolveAllowedFile(path.join(outside, 'secret.png'), '/', [allowed])).rejects.toThrow(/Refusing to attach/);
    await expect(resolveAllowedFile('../outside/secret.png', allowed, [allowed])).rejects.toThrow(/Refusing to attach/);
    await symlink(path.join(outside, 'secret.png'), path.join(allowed, 'link.png'));
    await expect(resolveAllowedFile(path.join(allowed, 'link.png'), '/', [allowed])).rejects.toThrow(/Refusing to attach/);
    // A root prefix is not enough: /x/allowed-evil is not under /x/allowed.
    await mkdir(`${allowed}-evil`);
    await writeFile(`${allowed}-evil/a.png`, 'x');
    await expect(resolveAllowedFile(`${allowed}-evil/a.png`, '/', [allowed])).rejects.toThrow(/Refusing to attach/);
  });

  it('accepts a symlink that resolves inside a root', async () => {
    await symlink(path.join(allowed, 'shot.png'), path.join(outside, 'into.png'));
    await expect(resolveAllowedFile(path.join(outside, 'into.png'), '/', [allowed])).resolves.toBe(path.join(allowed, 'shot.png'));
  });

  it('reports missing files, directories and NUL bytes', async () => {
    await expect(resolveAllowedFile(path.join(allowed, 'nope.png'), '/', [allowed])).rejects.toThrow(/File not found/);
    await mkdir(path.join(allowed, 'dir'));
    await expect(resolveAllowedFile(path.join(allowed, 'dir'), '/', [allowed])).rejects.toThrow(/Not a regular file/);
    await expect(resolveAllowedFile('a\0b', '/', [allowed])).rejects.toThrow(/Invalid path/);
  });

  it('ignores roots that do not exist', async () => {
    await expect(resolveAllowedFile(path.join(allowed, 'shot.png'), '/', ['/nonexistent-root', allowed])).resolves.toBeTruthy();
  });

  it('reads the head of a file', async () => {
    await writeFile(path.join(allowed, 'h'), Uint8Array.from([1, 2, 3]));
    expect(Array.from(await readHead(path.join(allowed, 'h'), 16))).toEqual([1, 2, 3]);
  });
});
