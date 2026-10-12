import { describe, expect, it } from 'bun:test'
import {
  BROWSER_CAPABILITIES,
  BROWSER_SUPPORTED_ACTIONS,
  X_SCIENCE_LINK_POLICY_VERSION,
  X_SCIENCE_LINK_PROTOCOL_VERSION,
  buildBrowserRuntimePolicy,
  buildExtensionRuntimeMetadata,
  buildHelloPayload,
  buildLocalDoctorReport,
} from './link-runtime.js'
import {
  buildPermissionSnapshot,
  countAuthorizedLocalFolders,
} from './permission-snapshot.js'

describe('X-Science Link extension runtime helpers', () => {
  it('builds hello metadata from the shared protocol and capability lists', () => {
    const runtime = buildExtensionRuntimeMetadata({
      manifest: { version: '0.6.10', manifest_version: 3 },
      startedAt: 123,
      lastHeartbeatAt: 456,
    })
    const policy = buildBrowserRuntimePolicy({
      maxXScienceTabs: 20,
      defaultSearchReadPages: 1,
      maxSearchReadPages: 2,
      maxHumanReadScrolls: 18,
      maxVisibleElements: 160,
    })

    const hello = buildHelloPayload({
      state: { clientId: 'client-1', name: '' },
      browser: 'Chrome',
      userAgent: 'test-agent',
      runtime,
      runtimePolicy: policy,
    })

    expect(hello).toMatchObject({
      type: 'hello',
      clientId: 'client-1',
      name: 'X-Science Link',
      protocolVersion: X_SCIENCE_LINK_PROTOCOL_VERSION,
      policyVersion: X_SCIENCE_LINK_POLICY_VERSION,
      extensionVersion: '0.6.10',
      runtimePolicy: {
        approvalScope: 'conversation',
        maxSearchReadPages: 2,
      },
    })
    expect(hello.supportedActions).toContain('run_task_loop')
    expect(hello.supportedActions).toEqual([...BROWSER_SUPPORTED_ACTIONS])
    expect(hello.capabilities).toEqual([...BROWSER_CAPABILITIES])
  })

  it('projects a local doctor report without depending on chrome globals', () => {
    const runtime = buildExtensionRuntimeMetadata({
      manifest: { version: '0.6.10', manifest_version: 3 },
      startedAt: 123,
      lastHeartbeatAt: null,
    })
    const policy = buildBrowserRuntimePolicy({
      maxXScienceTabs: 20,
      defaultSearchReadPages: 1,
      maxSearchReadPages: 2,
      maxHumanReadScrolls: 18,
      maxVisibleElements: 160,
    })

    const report = buildLocalDoctorReport({
      statusSnapshot: {
        connected: false,
        serverUrl: 'http://127.0.0.1:3456',
        clientId: 'client-1',
      },
      runtime,
      runtimePolicy: policy,
      permissions: buildPermissionSnapshot({
        bookmarks: false,
        localFiles: { authorizedFolders: [{ id: 'folder-1' }] },
      }),
      warnings: ['offline'],
    })

    expect(report).toMatchObject({
      status: 'degraded',
      server: {
        connectionStatus: 'offline',
        protocolVersion: X_SCIENCE_LINK_PROTOCOL_VERSION,
      },
      client: {
        clientKind: 'browser',
        clientId: 'client-1',
      },
      permissions: {
        bookmarks: false,
        localFiles: true,
        downloads: true,
      },
      warnings: ['offline'],
    })
  })

  it('counts authorized local folders across legacy and current shapes', () => {
    expect(countAuthorizedLocalFolders({ authorizedFolders: 2 })).toBe(2)
    expect(countAuthorizedLocalFolders({ authorizedFolders: [{}, {}] })).toBe(2)
    expect(countAuthorizedLocalFolders({ folders: [{}, {}, {}] })).toBe(3)
    expect(countAuthorizedLocalFolders({ authorizedFolders: -1 })).toBe(0)
    expect(countAuthorizedLocalFolders(null)).toBe(0)
  })
})
