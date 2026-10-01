import { describe, expect, it } from 'vitest';

import {
  getSynonBiomedArtifactImageFilename,
  getSynonBiomedArtifactReferenceId,
  getSynonBiomedRelativeArtifactFilename,
  resolveSynonBiomedArtifactRootFrameId,
  resolveSynonBiomedArtifactFile,
} from '@/renderer/pages/conversation/Messages/useSynonBiomedArtifactLinkPreview';
import type { ISynonBiomedScientificFile } from '@/common/adapter/ipcBridge';
import type { ArtifactReferenceWire } from '@/common/adapter/messageStreamProtocol';
import type { ConversationArtifactIndex } from '@/renderer/pages/conversation/Messages/artifacts';
import {
  createSynonBiomedCompanionArtifactUrls,
  parseSynonBiomedArtifactLink,
} from '@/renderer/services/synonBiomedArtifactReferences';

describe('Synon Biomed artifact link preview model', () => {
  it.each([
    '/api/artifacts/artifact-report/versions/version-report',
    'https://synon.bio/api/artifacts/artifact-report/versions/version-report?download=1#page=2',
    'http://another-deployment.test:8765/api/artifacts/artifact-report/versions/version-report',
    '#/artifacts/artifact-report/versions/version-report',
    '/#/artifacts/artifact-report/versions/version-report',
    'http://127.0.0.1:8765/#/artifacts/artifact-report/versions/version-report?download=1',
  ])('extracts only the exact artifact identity from %s', (href) => {
    expect(parseSynonBiomedArtifactLink(href)).toEqual({
      kind: 'content',
      artifactId: 'artifact-report',
      versionId: 'version-report',
    });
  });

  it.each([
    ['artifact%2Fwith%20space', 'version%2Fwith%20space', 'artifact/with space', 'version/with space'],
    ['%E6%8A%A5%E5%91%8A%2F%E5%9B%BE%20A', '%E7%89%88%E6%9C%AC%20%CE%B1', '报告/图 A', '版本 α'],
    ['a%2fv', 'v', 'a/v', 'v'],
    ['a%5cv', 'v', 'a\\v', 'v'],
    ['a%252fv', 'v%252F1', 'a%2fv', 'v%2F1'],
    ['report+edition%23%3F%25', 'v@1:2', 'report+edition#?%', 'v@1:2'],
    ['a%2F..%2Fb', 'v', 'a/../b', 'v'],
  ])(
    'decodes opaque escaped ID %s once without turning its contents into URL structure',
    (artifact, version, artifactId, versionId) => {
      const path = `/api/artifacts/${artifact}/versions/${version}`;
      for (const origin of ['', 'https://untrusted-origin.test']) {
        expect(parseSynonBiomedArtifactLink(`${origin}${path}?download=1#page=2`)).toEqual({
          kind: 'content',
          artifactId,
          versionId,
        });
      }
      expect(parseSynonBiomedArtifactLink(`/api/artifacts/${artifact}`)).toEqual({ kind: 'content', artifactId });
    }
  );

  it('classifies all generated-file link forms through one parser', () => {
    expect(parseSynonBiomedArtifactLink('/api/artifacts/artifact-report')).toEqual({
      kind: 'content',
      artifactId: 'artifact-report',
    });
    expect(parseSynonBiomedArtifactLink('%7B%7Bartifact%3Aversion-report%7D%7D')).toEqual({
      kind: 'reference',
      referenceId: 'version-report',
    });
    expect(parseSynonBiomedArtifactLink('./reports/report.pdf')).toEqual({ kind: 'filename', filename: 'report.pdf' });
  });

  it.each([
    '#/artifacts/',
    '/#/artifacts/',
    'http://127.0.0.1:8765/#/artifacts/',
    'https://untrusted.test/#/artifacts/',
  ])('normalizes hash-route identities without trusting an origin: %s', (prefix) => {
    expect(parseSynonBiomedArtifactLink(`${prefix}artifact%2Fwith%20space`)).toEqual({
      kind: 'content',
      artifactId: 'artifact/with space',
    });
    expect(parseSynonBiomedArtifactLink(`${prefix}a%252Fv/versions/v%2F1`)).toEqual({
      kind: 'content',
      artifactId: 'a%2Fv',
      versionId: 'v/1',
    });
    for (const identity of ['..', '%2e%2e', '%00', 'a%', 'a/../b', 'a/versions/%20', 'a/versions/v/extra']) {
      expect(parseSynonBiomedArtifactLink(`${prefix}${identity}`)).toBeNull();
    }
  });

  it.each([
    'https://example.test/report.pdf',
    '//synon.bio/api/artifacts/a/versions/v',
    'https://user:password@synon.bio/api/artifacts/a/versions/v',
    'https://synon.bio/api/artifacts/a/versions/v/extra',
    'https://synon.bio/api/artifacts/a/versions/v/',
    '/api/artifacts/a/../a/versions/v',
    '/api/artifacts/a/%2e%2e/a/versions/v',
    '/api/artifacts/./versions/v',
    '/api/artifacts/../versions/v',
    '/api/artifacts/%2e/versions/v',
    '/api/artifacts/%2E%2e/versions/v',
    '/api/artifacts/a/versions/%2E.',
    '/api/artifacts/a/versions/%20',
    '/api/artifacts/a/versions/v%',
    '/api/artifacts/a%2/versions/v',
    '/api/artifacts/a%GG/versions/v',
    '/api/artifacts/a/versions/%C0%AF',
    '/api/artifacts/a/versions/%ED%A0%80',
    '/api/artifacts/a/versions/v%00',
    '/api/artifacts/a%0Av/versions/v',
    '/api/artifacts/a/versions/v%7F',
    '/api/artifacts/a/versions/v%C2%85',
    'javascript:/api/artifacts/a/versions/v',
    'https://user:password@synon.bio/#/artifacts/a',
    'https://synon.bio/other/#/artifacts/a',
    'https://synon.bio/other/../#/artifacts/a',
    'javascript://synon.bio/#/artifacts/a',
    '//synon.bio/#/artifacts/a',
    '#/conversation/a',
  ])('does not promote an unsafe or unrelated URL into a local artifact: %s', (href) => {
    expect(parseSynonBiomedArtifactLink(href)).toBeNull();
  });

  it('extracts raw and URL-encoded artifact references', () => {
    const artifactId = 'f0af1025-7698-432a-b4ad-841099d2cd4b';

    expect(getSynonBiomedArtifactReferenceId(`{{artifact:${artifactId}}}`)).toBe(artifactId);
    expect(getSynonBiomedArtifactReferenceId(`%7B%7Bartifact%3A${artifactId}%7D%7D`)).toBe(artifactId);
    expect(getSynonBiomedArtifactReferenceId('{{artifact:}}')).toBeNull();
    expect(getSynonBiomedArtifactReferenceId('https://example.test/file.csv')).toBeNull();
  });

  it('keeps filename fallback resolution constrained to safe relative paths', () => {
    expect(getSynonBiomedRelativeArtifactFilename('reports/docking_results.csv?download=1')).toBe(
      'docking_results.csv'
    );
    expect(getSynonBiomedRelativeArtifactFilename('../docking_results.csv')).toBeNull();
    expect(getSynonBiomedRelativeArtifactFilename('/absolute/docking_results.csv')).toBeNull();
    expect(getSynonBiomedArtifactImageFilename('./molecule_grid.png')).toBe('molecule_grid.png');
  });

  it('indexes one unambiguous companion URL and rejects ambiguous or unavailable filenames', () => {
    expect(
      createSynonBiomedCompanionArtifactUrls([
        { name: 'report.md', contentUrl: '/report' },
        {
          name: 'plots',
          children: [
            { name: 'curve.png', contentUrl: '/curve' },
            { name: 'duplicate.csv', contentUrl: '/first' },
            { name: 'duplicate.csv', contentUrl: '/second' },
            { name: 'deleted.csv', contentUrl: '/deleted', availability: 'deleted' },
          ],
        },
      ])
    ).toEqual({ 'report.md': '/report', 'curve.png': '/curve' });
  });

  it('uses the exact message artifact version before a conversation-wide filename fallback', () => {
    const oldFile = {
      artifact_id: 'artifact-report',
      version_id: 'version-old',
      filename: 'final_report.md',
    } as ISynonBiomedScientificFile;
    const newFile = {
      artifact_id: 'artifact-report',
      version_id: 'version-new',
      filename: 'final_report.md',
    } as ISynonBiomedScientificFile;
    const index: ConversationArtifactIndex = {
      byFilename: new Map([['final_report.md', newFile]]),
      byArtifactId: new Map([['artifact-report', newFile]]),
      byVersionId: new Map([
        ['version-old', oldFile],
        ['version-new', newFile],
      ]),
    };
    const references = [
      {
        artifact_id: 'artifact-report',
        version_id: 'version-old',
        relation: 'produced',
        availability: 'available',
      } as ArtifactReferenceWire,
    ];

    expect(resolveSynonBiomedArtifactFile(index, null, 'final_report.md', references)).toBe(oldFile);
    expect(resolveSynonBiomedArtifactFile(index, 'version-old', null, references)).toBe(oldFile);
  });

  it('keeps the artifact root frame for unified preview operations', () => {
    expect(
      resolveSynonBiomedArtifactRootFrameId({ root_frame_id: 'root-1', frame_id: 'frame-1' }, 'conversation-1')
    ).toBe('root-1');
    expect(resolveSynonBiomedArtifactRootFrameId({ root_frame_id: null, frame_id: 'frame-1' }, 'conversation-1')).toBe(
      'frame-1'
    );
    expect(resolveSynonBiomedArtifactRootFrameId({ root_frame_id: null, frame_id: null }, 'conversation-1')).toBe(
      'conversation-1'
    );
  });

  it('does not substitute a cached newer version for an unresolved historical message reference', () => {
    const current = {
      artifact_id: 'artifact-report',
      version_id: 'version-new',
      filename: 'report.pdf',
    } as ISynonBiomedScientificFile;
    const index: ConversationArtifactIndex = {
      byFilename: new Map([['report.pdf', current]]),
      byArtifactId: new Map([['artifact-report', current]]),
      byVersionId: new Map([['version-new', current]]),
    };
    expect(
      resolveSynonBiomedArtifactFile(index, null, 'report.pdf', [
        {
          artifact_id: 'artifact-report',
          version_id: 'version-old',
        },
      ])
    ).toBeNull();
    expect(
      resolveSynonBiomedArtifactFile(index, null, 'report.pdf', [
        {
          artifact_id: 'another-artifact',
          version_id: 'version-new',
        },
      ])
    ).toBeNull();
  });

  it('keeps unavailable named references closed without blocking an available unrelated file', () => {
    const file = {
      artifact_id: 'artifact-report',
      version_id: 'version-report',
      filename: 'report.pdf',
    } as ISynonBiomedScientificFile;
    const index: ConversationArtifactIndex = {
      byFilename: new Map([['report.pdf', file]]),
      byArtifactId: new Map([['artifact-report', file]]),
      byVersionId: new Map([['version-report', file]]),
    };
    const available: ArtifactReferenceWire = {
      artifact_id: 'artifact-report',
      version_id: 'version-report',
      relation: 'produced',
    };
    const missing: ArtifactReferenceWire = {
      artifact_id: 'artifact-missing',
      version_id: 'version-missing',
      filename: 'other.pdf',
      relation: 'produced',
    };
    expect(resolveSynonBiomedArtifactFile(index, null, 'report.pdf', [available, missing], undefined, true)).toBe(file);
    expect(
      resolveSynonBiomedArtifactFile(
        index,
        null,
        'report.pdf',
        [{ ...available, version_id: 'version-old', filename: 'report.pdf', availability: 'deleted' }],
        undefined,
        true
      )
    ).toBeNull();
    const ambiguous = { ...file, artifact_id: 'artifact-other', version_id: 'version-other' };
    index.byVersionId.set(ambiguous.version_id, ambiguous);
    expect(
      resolveSynonBiomedArtifactFile(
        index,
        null,
        'report.pdf',
        [available, { artifact_id: ambiguous.artifact_id, version_id: ambiguous.version_id, relation: 'produced' }],
        undefined,
        true
      )
    ).toBeNull();
  });
});
