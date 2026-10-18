import { loadXScienceKernels } from '@/renderer/services/xScienceNotebook';
import { describe, expect, it, vi } from 'vitest';

const target = {
  schema: 'x-science.manual-kernel-target.v1',
  frame_id: 'frame-1',
  root_frame_id: 'root-1',
  frame_incarnation_id: 'frame-incarnation-1',
  root_frame_incarnation_id: 'root-incarnation-1',
  kernel_id: 'kernel-1',
  kernel_generation: 7,
  language: 'python',
  environment: 'python',
};

async function readTarget(manualTarget: unknown) {
  const fetchImpl = vi.fn(
    async () =>
      new Response(
        JSON.stringify({
          has_history: true,
          kernels: [
            {
              frame_id: 'frame-1',
              root_frame_id: 'root-1',
              project_id: 'project-1',
              kernel_id: 'kernel-1',
              language: 'python',
              environment: 'python',
              kind: 'analysis',
              busy: false,
              starting: false,
              pid_visible: true,
              // Neither this opaque runtime identifier nor history can create authority.
              runtime_generation: 'unrelated-runtime-version',
              execution_count: 4,
              manual_execution_target: manualTarget,
            },
          ],
        }),
        { headers: { 'content-type': 'application/json' } }
      )
  );
  const inventory = await loadXScienceKernels('root-1', { fetchImpl });
  expect(fetchImpl).toHaveBeenCalledWith(
    '/api/frames/root-1/kernels',
    expect.objectContaining({ credentials: 'include' })
  );
  return inventory.kernels[0];
}

describe('manual Notebook inventory target', () => {
  it('retains the complete matching local target without substituting runtime_generation', async () => {
    expect(await readTarget(target)).toMatchObject({
      manualExecutionTarget: {
        schema: target.schema,
        frameId: 'frame-1',
        rootFrameId: 'root-1',
        frameIncarnationId: 'frame-incarnation-1',
        rootFrameIncarnationId: 'root-incarnation-1',
        kernelId: 'kernel-1',
        kernelGeneration: 7,
        language: 'python',
        environment: 'python',
      },
    });
  });

  it.each([
    ['missing capability', undefined],
    ['unknown schema', { ...target, schema: 'not-an-execution-capability' }],
    ['different frame', { ...target, frame_id: 'other-frame' }],
    ['different root', { ...target, root_frame_id: 'other-root' }],
    ['different kernel', { ...target, kernel_id: 'other-kernel' }],
    ['different environment', { ...target, environment: 'other-environment' }],
    ['missing incarnation', { ...target, frame_incarnation_id: '' }],
    ['unsafe generation', { ...target, kernel_generation: Number.MAX_SAFE_INTEGER + 1 }],
    ['string generation', { ...target, kernel_generation: '7' }],
  ])('does not manufacture execution authority from %s', async (_name, manualTarget) => {
    expect(await readTarget(manualTarget)).not.toHaveProperty('manualExecutionTarget', expect.any(Object));
  });
});
