import { expect, test, type Locator } from '@playwright/test';
import { createHash } from 'node:crypto';
import { webPassword, webUsername } from './synonGoWebCredentials';

const viewports = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'narrow', width: 390, height: 844 },
] as const;

const expectTransparentImage = async (image: Locator, requireVisible = true) => {
  if (requireVisible) await expect(image).toBeVisible();
  else await expect(image).toBeAttached();
  await expect.poll(() => image.evaluate((node: HTMLImageElement) => node.naturalWidth)).toBeGreaterThan(0);

  const alpha = await image.evaluate((node: HTMLImageElement) => {
    const canvas = document.createElement('canvas');
    canvas.width = node.naturalWidth;
    canvas.height = node.naturalHeight;
    const context = canvas.getContext('2d', { willReadFrequently: true });
    if (!context) throw new Error('2D canvas is unavailable');
    context.drawImage(node, 0, 0);
    return context.getImageData(0, 0, 1, 1).data[3];
  });

  expect(alpha).toBe(0);
};

const expectApprovedMark = async (image: Locator) => {
  await expectTransparentImage(image);
  const source = await image.evaluate((node: HTMLImageElement) => node.currentSrc);
  const response = await image.page().request.get(source);
  expect(response.ok()).toBe(true);
  expect(createHash('sha256').update(await response.body()).digest('hex')).toBe(
    '627c51b57fe4f46f91a3d1effcd87d2698c006da82e8770ffee6404edaeb50fa'
  );
};

for (const viewport of viewports) {
  test.describe(viewport.name, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test('shows the transparent X-Science brand before and after authentication', async ({ page }) => {
      await page.goto('/#/login', { waitUntil: 'domcontentloaded' });

      await expect(page).toHaveTitle('X-Science');
      await expectApprovedMark(page.locator('.login-page__logo img'));
      await expect(page.getByRole('heading', { name: 'X-Science', exact: true })).toBeVisible();
      await expect(page.locator('body')).not.toContainText(/SynonAI/i);

      await page.locator('input[name="username"]').fill(webUsername);
      await page.locator('input[name="password"]').fill(webPassword);
      await page.locator('button[type="submit"]').click();
      await expect(page).toHaveURL(/#\/guid/);

      await expect(page).toHaveTitle('X-Science');
      const brand = page.getByTestId('synon-biomed-brand-lockup');
      await expectTransparentImage(brand.locator('img'), viewport.name === 'desktop');
      await expect(brand).toContainText('X-Science');
      await expect(page.locator('body')).not.toContainText(/SynonAI/i);
    });
  });
}
