import { describe, expect, it } from 'vitest';
import type { TChatConversation } from '@/common/config/storage';
import {
  conversationPreviewWorkspace,
  previewRouteWorkspace,
} from '@/renderer/pages/conversation/Preview/context/previewRouteWorkspace';

describe('preview route identity projection', () => {
  it('leaves a conversation unresolved until its own source data arrives', () => {
    expect(previewRouteWorkspace('owner', { pathname: '/conversation/session-b', key: 'navigation' })).toMatchObject({
      ownerId: 'owner',
      conversationId: 'session-b',
      projectId: undefined,
    });
    expect(
      conversationPreviewWorkspace('session-b', { id: 'session-a', extra: { project_id: 'a' } } as TChatConversation)
    ).toBeNull();
    expect(
      conversationPreviewWorkspace('session-b', {
        id: 'session-b',
        extra: { project_id: 'b', workspace: 'x-science://project/b' },
      } as TChatConversation)
    ).toEqual({ projectId: 'b', workspaceId: 'x-science://project/b' });
  });

  it('uses exact route/project URI ids and never a display title or old preferred project', () => {
    expect(
      previewRouteWorkspace('owner', { pathname: '/conversation/session%20a', key: 'session' }).conversationId
    ).toBe('session a');
    expect(previewRouteWorkspace('owner', { pathname: '/projects/project%20a', key: 'project' }).projectId).toBe(
      'project a'
    );
    expect(
      previewRouteWorkspace('owner', {
        pathname: '/guid',
        key: 'guid',
        state: { workspace: 'x-science://project/project%20a?name=Another+project' },
      }).projectId
    ).toBe('project a');
    expect(
      previewRouteWorkspace('owner', {
        pathname: '/guid',
        key: 'guid',
        state: { workspace: 'x-science://project/%zz' },
      }).projectId
    ).toBeUndefined();
  });

  it('separates Library and unassigned routes and gives a new navigation a new binding token', () => {
    expect(previewRouteWorkspace('owner', { pathname: '/library', key: 'library' }).area).toBe('library');
    expect(previewRouteWorkspace('owner', { pathname: '/guid', key: 'guid' }).area).toBe('unassigned');
    expect(previewRouteWorkspace('owner', { pathname: '/conversation/a', key: 'one' }).routeKey).not.toBe(
      previewRouteWorkspace('owner', { pathname: '/conversation/a', key: 'two' }).routeKey
    );
  });
});
