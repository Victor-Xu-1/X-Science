import { expect, it, vi } from 'vitest';
import { loadXScienceAnnotationTarget, createXScienceAnnotationTarget } from '@/renderer/services/xScienceAnnotations';

it('uses one canonical decoder across bounded Library pages and source-bound creation', async () => {
  const checksum = 'a'.repeat(64);
  const note = { id: 'note', text: 'Note', type: 'point', page_number: 1, x_percent: 2, y_percent: 3 };
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          target_key: 'litv:litv_one',
          current_checksum: checksum,
          annotations: [note],
          next_cursor: 'next',
        })
      )
    )
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          target_key: 'litv:litv_one',
          current_checksum: checksum,
          annotations: [{ ...note, id: 'note2' }],
          next_cursor: null,
        })
      )
    )
    .mockResolvedValueOnce(new Response(JSON.stringify(note), { status: 201 }));
  const target = { kind: 'literature', versionId: 'litv_one' } as const;
  const result = await loadXScienceAnnotationTarget(target, fetch);
  expect(result.annotations.map((value) => value.id)).toEqual(['note', 'note2']);
  expect(fetch.mock.calls[1][0]).toBe('/api/literature/attachment-versions/litv_one/annotations?cursor=next');
  await createXScienceAnnotationTarget(
    target,
    { type: 'point', text: 'Note', pageNumber: 1, xPercent: 2, yPercent: 3 },
    result.currentChecksum,
    fetch
  );
  expect(JSON.parse(fetch.mock.calls[2][1].body)).toMatchObject({ content_checksum: checksum, page_number: 1 });
});
