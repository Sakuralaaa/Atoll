import { expect, test } from '@playwright/test'

test('migration chooser leads to both wizards', async ({ page }) => {
  await page.goto('/?demo=1#/import')
  await page.getByTestId('import-way-zeabur').click()
  await expect(page).toHaveURL(/from=zeabur/)
  await page.goto('/?demo=1#/import')
  await page.getByTestId('import-way-docker').click()
  await expect(page).toHaveURL(/\/import\/docker/)
})

test('long server list and history show five rows, then scroll', async ({ page }) => {
  await page.goto('/?demo=1&stress=1#/import?from=zeabur')
  for (const id of ['import-server-list', 'import-history']) {
    const box = page.getByTestId(id)
    await expect(box).toBeVisible()
    const [n, h, sh, row5] = await box.evaluate(e => { const r = e.children[4] as HTMLElement; return [e.children.length, e.clientHeight, e.scrollHeight, r.offsetTop + r.offsetHeight] })
    expect(n).toBeGreaterThan(5)
    expect(sh).toBeGreaterThan(h)
    expect(Math.abs(h - row5)).toBeLessThanOrEqual(2)
  }
})

test('stepper rule reaches the last dot', async ({ page }) => {
  await page.goto('/?demo=1#/import?from=zeabur')
  const steps = page.getByLabel('steps').locator('li')
  const n = await steps.count()
  const rule = await steps.nth(n - 2).locator('span.flex-1').boundingBox()
  const dot = await steps.nth(n - 1).locator('span').first().boundingBox()
  expect(dot!.x - (rule!.x + rule!.width)).toBeLessThanOrEqual(12)
})
