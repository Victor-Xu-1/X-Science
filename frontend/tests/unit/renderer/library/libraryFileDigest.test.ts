import { createHash } from 'node:crypto';
import { sha256 } from '@noble/hashes/sha2.js';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { digestLibraryFile } from '@/renderer/services/runtime/libraryFileDigest';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function trackHasher() {
  const hasher = sha256.create();
  const prototype = Object.getPrototypeOf(hasher) as { destroy: () => void };
  hasher.destroy();
  const destroy = vi.spyOn(prototype, 'destroy');
  return { destroy };
}

describe('library File continuity digest', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('matches known SHA-256 vectors', async () => {
    for (const [text, expected] of [
      ['', 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'],
      ['abc', 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'],
      [
        'abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq',
        '248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1',
      ],
    ]) {
      const digest = await digestLibraryFile(new Blob([text]));
      expect(digest).toBe(expected);
      expect(digest).toMatch(/^[0-9a-f]{64}$/);
    }
  });

  it('hashes multiple bounded slices with one read in flight', async () => {
    const bytes = Uint8Array.from({ length: 1024 * 1024 + 17 }, (_, index) => index % 251);
    const file = new Blob([bytes]);
    const originalSlice = file.slice.bind(file);
    vi.spyOn(file, 'arrayBuffer').mockRejectedValue(new Error('Whole-file read is forbidden'));
    let active = 0;
    let maximumActive = 0;
    const windows: number[] = [];
    vi.spyOn(file, 'slice').mockImplementation((start, end, type) => {
      const part = originalSlice(start, end, type);
      const read = part.arrayBuffer.bind(part);
      windows.push(part.size);
      vi.spyOn(part, 'arrayBuffer').mockImplementation(async () => {
        active += 1;
        maximumActive = Math.max(maximumActive, active);
        try {
          return await read();
        } finally {
          active -= 1;
        }
      });
      return part;
    });
    const digest = await digestLibraryFile(file);
    expect(digest).toBe(createHash('sha256').update(bytes).digest('hex'));
    expect(windows.length).toBeGreaterThan(1);
    expect(Math.max(...windows)).toBeLessThanOrEqual(256 * 1024);
    expect(windows.reduce((sum, size) => sum + size, 0)).toBe(file.size);
    expect(maximumActive).toBe(1);
    expect(active).toBe(0);
    expect(file.arrayBuffer).not.toHaveBeenCalled();
  });

  it('distinguishes same-name same-size files with different bytes', async () => {
    const first = new File(['first'], 'reference.pdf', { type: 'application/pdf', lastModified: 1 });
    const second = new File(['other'], 'reference.pdf', { type: 'application/pdf', lastModified: 1 });
    const renamed = new File([first], 'renamed.pdf', { type: 'application/octet-stream', lastModified: 2 });
    expect(first.size).toBe(second.size);
    expect(await digestLibraryFile(first)).not.toBe(await digestLibraryFile(second));
    expect(await digestLibraryFile(first)).toBe(await digestLibraryFile(renamed));
  });

  it('rejects a short slice read and destroys its hasher', async () => {
    const { destroy } = trackHasher();
    const file = new Blob(['abc']);
    const slice = new Blob(['abc']);
    vi.spyOn(slice, 'arrayBuffer').mockResolvedValue(new ArrayBuffer(2));
    vi.spyOn(file, 'slice').mockReturnValue(slice);
    await expect(digestLibraryFile(file)).rejects.toThrow('File digest read was incomplete');
    expect(destroy).toHaveBeenCalled();
  });

  it('does not read an already aborted Blob', async () => {
    const file = new Blob(['abc']);
    const read = vi.spyOn(file, 'slice');
    const abort = new AbortController();
    abort.abort();
    await expect(digestLibraryFile(file, abort.signal)).rejects.toMatchObject({ name: 'AbortError' });
    expect(read).not.toHaveBeenCalled();
  });

  it('settles abort before a delayed read fulfills and never reads again', async () => {
    const { destroy } = trackHasher();
    const file = new Blob([new Uint8Array(512 * 1024)]);
    const part = new Blob([new Uint8Array(256 * 1024)]);
    const started = deferred<void>();
    const read = deferred<ArrayBuffer>();
    vi.spyOn(part, 'arrayBuffer').mockImplementation(() => {
      started.resolve();
      return read.promise;
    });
    const slices = vi.spyOn(file, 'slice').mockReturnValue(part);
    const abort = new AbortController();
    const result = digestLibraryFile(file, abort.signal);
    const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
    await started.promise;
    abort.abort();
    await rejected;
    expect(destroy).toHaveBeenCalled();
    expect(slices).toHaveBeenCalledTimes(1);
    read.resolve(new ArrayBuffer(part.size));
    await Promise.resolve();
    await Promise.resolve();
    expect(slices).toHaveBeenCalledTimes(1);
  });

  it('observes a delayed read rejection after abort', async () => {
    const file = new Blob(['abc']);
    const part = new Blob(['abc']);
    const started = deferred<void>();
    const read = deferred<ArrayBuffer>();
    vi.spyOn(part, 'arrayBuffer').mockImplementation(() => {
      started.resolve();
      return read.promise;
    });
    const slices = vi.spyOn(file, 'slice').mockReturnValue(part);
    const unhandled = vi.fn();
    process.on('unhandledRejection', unhandled);
    try {
      const abort = new AbortController();
      const result = digestLibraryFile(file, abort.signal);
      const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
      await started.promise;
      abort.abort();
      await rejected;
      read.reject(new Error('Late Blob read failure'));
      await new Promise<void>((resolve) => setImmediate(resolve));
      expect(unhandled).not.toHaveBeenCalled();
      expect(slices).toHaveBeenCalledTimes(1);
    } finally {
      process.off('unhandledRejection', unhandled);
    }
  });

  it('aborts during the cooperative yield before the next slice', async () => {
    vi.useFakeTimers();
    const file = new Blob([new Uint8Array(512 * 1024)]);
    const part = new Blob([new Uint8Array(256 * 1024)]);
    vi.spyOn(part, 'arrayBuffer').mockResolvedValue(new ArrayBuffer(part.size));
    const slices = vi.spyOn(file, 'slice').mockReturnValue(part);
    const abort = new AbortController();
    const result = digestLibraryFile(file, abort.signal);
    const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' });
    for (let turn = 0; turn < 8; turn += 1) await Promise.resolve();
    expect(vi.getTimerCount()).toBe(1);
    expect(slices).toHaveBeenCalledTimes(1);
    abort.abort();
    await rejected;
    await vi.runAllTimersAsync();
    expect(vi.getTimerCount()).toBe(0);
    expect(slices).toHaveBeenCalledTimes(1);
  });
});
