import { expect, test } from '@playwright/test';
import {
  createScientificWorkspace,
  inspectRenderedPixels,
  loginToScientificWorkbench,
  openScientificArtifact,
  removeScientificWorkspace,
  uploadScientificArtifact,
} from './synonBiomedScientificFixture';

const PDB_FIXTURE = `HEADER    SYNON BIOMED READ ONLY INTERACTION ACCEPTANCE
ATOM      1  ND2 ASN A  42       0.000   0.000   0.000  1.00 20.00           N
ATOM      2  CA  GLY A  43       3.800   3.000   0.000  1.00 20.00           C
HETATM    3  O1  ATP C 101       2.800   0.000   0.000  1.00 20.00           O
HETATM    4  C1  ATP C 101       0.000   3.000   0.000  1.00 20.00           C
CONECT    3    4
TER
END
`;

const PDB_ENSEMBLE_FIXTURE = `HEADER    SYNON BIOMED MULTI MODEL ACCEPTANCE
MODEL        1
HETATM    1  C1  LIG L   1      -1.450   0.000   0.000  1.00 20.00           C
HETATM    2  C2  LIG L   1       0.000   0.000   0.000  1.00 20.00           C
HETATM    3  O1  LIG L   1       1.250   0.700   0.000  1.00 20.00           O
HETATM    4  N1  LIG L   1       0.000  -1.350   0.000  1.00 20.00           N
CONECT    1    2
CONECT    2    1    3    4
CONECT    3    2
CONECT    4    2
ENDMDL
MODEL        2
HETATM    1  C1  LIG L   1      -1.450   0.000   0.500  1.00 20.00           C
HETATM    2  C2  LIG L   1       0.000   0.000   0.000  1.00 20.00           C
HETATM    3  O1  LIG L   1       0.900   1.050  -0.300  1.00 20.00           O
HETATM    4  N1  LIG L   1       0.350  -1.250   0.450  1.00 20.00           N
CONECT    1    2
CONECT    2    1    3    4
CONECT    3    2
CONECT    4    2
ENDMDL
END
`;

test.use({ viewport: { width: 1440, height: 900 } });

test('keeps structure preview read-only and displays interactions through the real Mol* renderer', async ({ page }) => {
  const structureErrors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error' && /SynonBiomedStructureViewer|Mol\*|removeChild/iu.test(message.text())) {
      structureErrors.push(message.text());
    }
  });
  page.on('pageerror', (error) => {
    if (/SynonBiomedStructureViewer|Mol\*|removeChild/iu.test(error.message)) structureErrors.push(error.message);
  });

  await loginToScientificWorkbench(page);
  const workspace = await createScientificWorkspace(page, 'structure-read-only-acceptance');
  try {
    const artifact = await uploadScientificArtifact(page, workspace, {
      filename: 'interaction-view.pdb',
      contentType: 'chemical/x-pdb',
      source: PDB_FIXTURE,
    });
    await openScientificArtifact(page, artifact.artifactId);

    const region = page.getByRole('region', { name: /3D 结构预览/u });
    await expect(region).toBeVisible();
    await region.getByRole('button', { name: '展开左侧工具栏', exact: true }).click();
    await expect(region.getByRole('complementary', { name: /表示层编辑器/u })).toHaveCount(0);
    await expect(region.getByRole('tab', { name: /Ligand 编辑/u })).toHaveCount(0);
    await expect(region.getByRole('button', { name: /添加图层/u })).toHaveCount(0);

    // One pocket action owns residue, interaction, and distance rendering;
    // the retired independent toggles must not reintroduce competing state.
    const pocketMode = region.getByTestId('synon-biomed-molstar-quick-pocket');
    await expect(pocketMode).toBeEnabled();
    if ((await pocketMode.getAttribute('aria-pressed')) === 'true') {
      await pocketMode.click();
      await expect(pocketMode).toHaveAttribute('aria-pressed', 'false');
    }
    await pocketMode.click();
    await expect(pocketMode).toHaveAttribute('aria-pressed', 'true');

    const canvas = region.locator('canvas');
    await expect(canvas).toHaveCount(1);
    await expect.poll(async () => (await inspectRenderedPixels(canvas)).nonWhite).toBeGreaterThan(100);
    const quickToolbar = region.getByTestId('synon-biomed-molstar-quick-toolbar');
    expect(await quickToolbar.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
    expect(structureErrors).toEqual([]);
  } finally {
    await removeScientificWorkspace(page, workspace);
  }
});

test('switches a multi-model complex inside the built-in 3D preview', async ({ page }) => {
  const structureErrors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error' && /SynonBiomedStructureViewer|Mol\*|removeChild/iu.test(message.text())) {
      structureErrors.push(message.text());
    }
  });
  page.on('pageerror', (error) => {
    if (/SynonBiomedStructureViewer|Mol\*|removeChild/iu.test(error.message)) structureErrors.push(error.message);
  });

  await loginToScientificWorkbench(page);
  const workspace = await createScientificWorkspace(page, 'structure-ensemble-acceptance');
  try {
    const artifact = await uploadScientificArtifact(page, workspace, {
      filename: 'ensemble.pdb',
      contentType: 'chemical/x-pdb',
      source: PDB_ENSEMBLE_FIXTURE,
    });
    await openScientificArtifact(page, artifact.artifactId);

    const region = page.getByRole('region', { name: /3D 结构预览/u });
    const modelNavigator = region.getByRole('group', { name: /结构模型/u });
    await expect(region).toBeVisible();
    await region.getByRole('button', { name: '展开左侧工具栏', exact: true }).click();
    await expect(modelNavigator).toBeVisible();
    await expect(modelNavigator).toContainText('模型 1 / 2');

    await modelNavigator.getByRole('button', { name: /下一个模型/u }).click();
    await expect(modelNavigator).toContainText('模型 2 / 2');

    const canvas = region.locator('canvas');
    await expect(canvas).toHaveCount(1);
    await expect.poll(async () => (await inspectRenderedPixels(canvas)).nonWhite).toBeGreaterThan(100);
    expect(structureErrors).toEqual([]);
  } finally {
    await removeScientificWorkspace(page, workspace);
  }
});

test('persists a native structure snapshot without changing its coordinate version', async ({ page }) => {
  await loginToScientificWorkbench(page);
  const workspace = await createScientificWorkspace(page, 'structure-snapshot');
  try {
    const structure = await uploadScientificArtifact(page, workspace, {
      filename: 'snapshot-source.pdb',
      contentType: 'chemical/x-pdb',
      source: PDB_FIXTURE,
    });
    const sourcePath = `/api/artifacts/${structure.artifactId}`;
    const versionsBefore = await (await page.request.get(`${sourcePath}/versions`)).json();
    await openScientificArtifact(page, structure.artifactId);
    const region = page.getByRole('region', { name: /3D 结构预览/u });
    await expect(region).toBeVisible();
    await region.getByRole('button', { name: '展开左侧工具栏', exact: true }).click();
    const capture = region.getByTestId('synon-biomed-molstar-quick-snapshot');
    await expect(capture).toBeEnabled();
    await expect
      .poll(async () => (await inspectRenderedPixels(region.locator('canvas'))).nonWhite)
      .toBeGreaterThan(100);
    const [savedResponse, download] = await Promise.all([
      page.waitForResponse((response) => {
        const url = new URL(response.url());
        return response.request().method() === 'POST' && url.pathname === `${sourcePath}/versions/binary`;
      }),
      page.waitForEvent('download'),
      capture.click(),
    ]);
    expect(savedResponse.status()).toBe(201);
    expect(new URL(savedResponse.url()).searchParams.get('parent_version_id')).toBe(structure.versionId);
    expect(download.suggestedFilename()).toBe('snapshot-source-snapshot.png');
    const saved = (await savedResponse.json()) as { artifact_id: string; version_id: string };
    expect(saved.artifact_id).not.toBe(structure.artifactId);
    expect(saved.version_id).not.toBe(structure.versionId);
    const imagePath = `/api/artifacts/${saved.artifact_id}/versions/${saved.version_id}`;
    const imageResponse = await page.request.get(imagePath);
    expect(imageResponse.status()).toBe(200);
    expect(imageResponse.headers()['content-type']).toContain('image/png');
    const imageBytes = await imageResponse.body();
    expect([...imageBytes.subarray(0, 8)]).toEqual([137, 80, 78, 71, 13, 10, 26, 10]);
    expect(imageBytes.length).toBeGreaterThan(1024);
    expect(await (await page.request.get(sourcePath)).text()).toBe(PDB_FIXTURE);
    expect(await (await page.request.get(`${sourcePath}/versions`)).json()).toEqual(versionsBefore);
    await page.reload({ waitUntil: 'domcontentloaded' });
    expect(await (await page.request.get(imagePath)).body()).toEqual(imageBytes);
    await expect(page.getByRole('region', { name: /3D 结构预览/u })).toBeVisible();
    await page.screenshot({
      path: test.info().outputPath('persisted-structure-snapshot.png'),
      fullPage: true,
    });
  } finally {
    await removeScientificWorkspace(page, workspace);
  }
});
