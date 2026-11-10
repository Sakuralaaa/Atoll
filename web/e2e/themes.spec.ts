import { expect, test } from '@playwright/test'

test('preset theme switches live, survives a reload and keeps light/dark separate', async ({ page }) => {
  await page.goto('/?demo=1#/settings/security')
  const html = page.locator('html')
  await expect(page.getByRole('radiogroup', { name: '预设主题' })).toBeVisible()
  await expect(html).toHaveAttribute('data-theme', 'atoll')

  await page.getByTestId('theme-minimal').click()
  await expect(html).toHaveAttribute('data-theme', 'minimal')
  await expect(page.getByTestId('theme-minimal')).toHaveAttribute('aria-checked', 'true')
  await page.getByRole('radio', { name: '白天' }).click()
  await expect(html).toHaveClass(/\blight\b/)

  await page.reload()
  await expect(html).toHaveAttribute('data-theme', 'minimal')
  await expect(html).toHaveClass(/\blight\b/)
  // the boot script applies the look before the app renders: no flash of the default theme
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--bw').trim())).toBe('1px')

  await page.getByTestId('theme-terminal').click()
  await expect(html).toHaveAttribute('data-theme', 'terminal')
  await page.getByTestId('theme-atoll').click()
  await expect(html).toHaveAttribute('data-theme', 'atoll')
})

test('?style= pins a theme for the page load', async ({ page }) => {
  await page.goto('/?demo=1&style=contrast&theme=dark#/')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'contrast')
  await expect(page.locator('html')).toHaveClass(/\bdark\b/)
})

test('a sub-account can pick a theme too', async ({ page }) => {
  await page.goto('/?demo=1&member=ro#/settings/security')
  await page.getByTestId('theme-macaron').click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'macaron')
})
