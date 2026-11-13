import { expect, test } from '@playwright/test'

test('a GHCR image links to its GitHub repo; other registries stay plain text', async ({ page }) => {
  await page.goto('/?demo=1#/projects/mailrelay/services/mail-relay')
  const link = page.getByTestId('image-repo')
  await expect(link).toHaveAttribute('href', 'https://github.com/example/mail-relay')
  await expect(link).toHaveAttribute('target', '_blank')

  await page.goto('/?demo=1#/projects/acme/services/acme-api')
  // built from source: the repo path links to the repo
  await expect(page.getByTestId('image-repo')).toHaveAttribute('href', 'https://github.com/example/acme-api')

  await page.goto('/?demo=1#/projects/acme/services/pg')
  await expect(page.getByText('postgres', { exact: true })).toBeVisible()
  await expect(page.getByTestId('image-repo')).toHaveCount(0)
})
