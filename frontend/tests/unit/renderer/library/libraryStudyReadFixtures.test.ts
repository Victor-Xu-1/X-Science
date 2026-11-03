import { describe, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { decodeLiteratureStudy, literatureStudySourceKey } from '@/common/adapter/literatureStudySnapshot';
import { readNotGeneratedLiteratureStudy } from './libraryStudyReadFixtures';

const item: LiteratureItem = {
  id: 'paper-a',
  title: 'Synthetic fixture paper',
  metadata_revision: 3,
  metadata: { abstract: 'Available synthetic abstract.' },
  deleted: false,
  project_ids: [],
  attachments: [],
};
const endpoint = '/api/literature/items/paper-a/study-snapshot';
const url = (value: string) => new URL(value, 'http://localhost');

describe('Study read fixture route contract', () => {
  it('returns a fresh complete source-bound read DTO without implying generation', async () => {
    const read = vi.fn(() => item);
    const first = readNotGeneratedLiteratureStudy(url(endpoint + '?language=en-US'), undefined, read)!;
    const second = readNotGeneratedLiteratureStudy(url(endpoint + '?language=zh-CN'), { method: 'GET' }, read)!;
    expect(first).not.toBe(second);
    expect(first.status).toBe(200);
    expect(decodeLiteratureStudy(await first.json(), item)).toEqual({
      schema: 'x-science.literature-study.v1',
      source_key: literatureStudySourceKey(item),
      revision: 0,
      available: true,
    });
    expect(decodeLiteratureStudy(await second.json(), item).snapshot).toBeUndefined();
    expect(read.mock.calls).toEqual([['paper-a'], ['paper-a']]);
    const noSource = { ...item, metadata: {} };
    const unavailable = readNotGeneratedLiteratureStudy(url(endpoint + '?language=en-US'), undefined, () => noSource)!;
    expect(decodeLiteratureStudy(await unavailable.json(), noSource).available).toBe(false);
  });

  it('does not absorb writes, other routes, invalid language queries or another item', () => {
    const read = vi.fn(() => item);
    for (const [target, method] of [
      [endpoint + '?language=en-US', 'POST'],
      ['/api/literature/items/paper-a?language=en-US', 'GET'],
      ['/api/literature/items/paper-a/publication-check?language=en-US', 'GET'],
      [endpoint, 'GET'],
      [endpoint + '?language=en-US&language=zh-CN', 'GET'],
      [endpoint + '?language=en-US&unexpected=1', 'GET'],
      [endpoint + '?language=%3Cunknown%3E', 'GET'],
    ])
      expect(readNotGeneratedLiteratureStudy(url(target), { method }, read)).toBeUndefined();
    expect(read).not.toHaveBeenCalled();
    expect(() =>
      readNotGeneratedLiteratureStudy(url(endpoint.replace('paper-a', 'paper-b') + '?language=en-US'), undefined, read)
    ).toThrow('Unexpected study fixture item');
  });
});
