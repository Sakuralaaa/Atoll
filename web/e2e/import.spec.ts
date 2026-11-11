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

test('long audit log and domain list scroll inside their box', async ({ page }) => {
  for (const [url, id] of [['/?demo=1&stress=1#/control?tab=audit', 'audit-list'], ['/?demo=1&stress=1#/domains', 'domain-list']]) {
    await page.goto(url)
    const box = page.getByTestId(id)
    await expect(box).toBeVisible()
    const [h, sh, vh] = await box.evaluate(e => [e.clientHeight, e.scrollHeight, window.innerHeight])
    expect(sh).toBeGreaterThan(h)
    expect(h).toBeLessThanOrEqual(Math.max(320, Math.round(vh * 0.6)) + 1)
  }
})

test('control migration shows one step at a time', async ({ page }) => {
  await page.goto('/?demo=1#/control?tab=migrate')
  const step = page.getByTestId('control-migrate-step')
  await expect(step).toContainText('atoll-server export')
  await page.getByRole('button', { name: '下一步' }).click()
  await expect(step).toContainText('--restore')
  await expect(step).not.toContainText('atoll-server export')
  await page.getByTestId('control-migrate-steps').getByRole('button', { name: '关闭旧中控' }).click()
  await expect(step).toContainText('systemctl disable --now atoll-server')
})

test('settings show one group at a time', async ({ page }) => {
  await page.goto('/?demo=1#/settings')
  const group = page.getByTestId('settings-group')
  await expect(group).toContainText('面板地址')
  await expect(group).not.toContainText('Zone ID')
  await page.getByTestId('settings-nav').getByRole('button', { name: '域名' }).click()
  await expect(group).toContainText('Zone ID')
  await expect(group).not.toContainText('面板地址')
  await page.goto('/?demo=1#/settings#backup')
  await expect(page.getByTestId('settings-nav').getByRole('button', { name: '备份' })).toHaveAttribute('aria-current', 'page')
})
