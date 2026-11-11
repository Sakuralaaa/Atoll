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

test('the old "zeabur" theme id opens as Lagoon', async ({ page }) => {
  await page.goto('/?demo=1#/')
  await page.evaluate(() => localStorage.setItem('atoll-style', 'zeabur'))
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'lagoon')
  await page.goto('/?demo=1&style=zeabur#/')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'lagoon')
})

test('the default theme has no coloured line across the top', async ({ page }) => {
  await page.goto('/?demo=1&style=atoll&theme=light#/')
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--topline-h').trim())).toBe('0px')
})

test('the sidebar shows the whole account role', async ({ page }) => {
  for (const style of ['atoll', 'contrast']) {
    await page.goto(`/?demo=1&style=${style}&theme=light#/`)
    const role = page.getByTestId('account-role')
    await expect(role).toHaveText('演示模式')
    // not clipped: the text fits the box it is drawn in
    expect(await role.evaluate(el => el.scrollWidth <= el.clientWidth + 1 && el.getBoundingClientRect().right <= (el.closest('aside') ?? document.body).getBoundingClientRect().right)).toBe(true)
  }
})

test('Lagoon and Macaron keep Atoll\'s block shape; Lagoon by day uses Terminal\'s greens', async ({ page }) => {
  const css = (name: string) => page.evaluate(n => getComputedStyle(document.documentElement).getPropertyValue(n).trim(), name)
  for (const style of ['lagoon', 'macaron']) {
    for (const mode of ['light', 'dark']) {
      await page.goto(`/?demo=1&style=${style}&theme=${mode}#/`)
      expect(await css('--bw')).toBe('2px')
      expect(await css('--sh-card')).not.toContain('rgba')
    }
  }
  await page.goto('/?demo=1&style=terminal&theme=light#/')
  const terminal = [await css('--bg'), await css('--brand')]
  await page.goto('/?demo=1&style=lagoon&theme=light#/')
  expect([await css('--bg'), await css('--brand')]).toEqual(terminal)
  await page.goto('/?demo=1&style=macaron&theme=light#/')
  expect(await css('--brand')).toBe('#d81b60')
})
