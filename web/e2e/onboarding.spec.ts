import { expect, test } from '@playwright/test'

test('first-run wizard: steps can be saved or skipped, progress is remembered, reopened from Settings', async ({ page }) => {
  await page.goto('/?demo=1&onboarding=1#/')
  const wizard = page.getByTestId('onboarding')
  await expect(wizard.getByRole('heading', { name: '欢迎使用 Atoll' })).toBeVisible({ timeout: 20_000 })
  await expect(page).toHaveURL(/#\/welcome/)

  // 1. name and public URL
  await wizard.getByPlaceholder('atoll-control').fill('e2e-control')
  await wizard.getByPlaceholder('https://panel.example.com').fill('https://panel.e2e.example.com')
  await wizard.getByRole('button', { name: '保存并继续' }).click()

  // 2. join command, demo servers are already connected
  await expect(wizard.getByRole('heading', { name: '添加第一台节点' })).toBeVisible()
  await expect(wizard.getByText(/install-agent\.sh/)).toBeVisible()
  await expect(wizard.getByTestId('onboarding-server-status')).toContainText('已连接')
  await wizard.getByRole('button', { name: '继续', exact: true }).click()

  // 3 + 4 optional steps are skipped
  await expect(wizard.getByRole('heading', { name: 'Cloudflare 根域名' })).toBeVisible()
  await wizard.getByRole('button', { name: '跳过这一步' }).click()
  await expect(wizard.getByRole('heading', { name: 'S3 备份' })).toBeVisible()

  // progress survives a reload (localStorage, per account + control server)
  await page.goto('/?demo=1#/')
  await expect(wizard.getByRole('heading', { name: 'S3 备份' })).toBeVisible()
  await wizard.getByRole('button', { name: '跳过这一步' }).click()

  // 5. deploy: template picker opens the create-project dialog; finishing returns to projects
  await expect(wizard.getByRole('heading', { name: '部署第一个服务' })).toBeVisible()
  await wizard.getByRole('button', { name: /从应用模板部署/ }).click()
  await expect(page.getByRole('button', { name: '创建并部署' })).toBeVisible()
  await page.getByRole('button', { name: '取消', exact: true }).click()
  await wizard.getByRole('button', { name: '完成引导' }).click()
  await expect(page.getByRole('heading', { name: '我的项目' })).toBeVisible()

  // finished: the projects page no longer redirects; Settings reopens it
  await page.goto('/?demo=1#/')
  await expect(page.getByRole('heading', { name: '我的项目' })).toBeVisible()
  await page.goto('/?demo=1#/settings')
  await page.getByRole('button', { name: '打开向导' }).click()
  await expect(wizard.getByRole('heading', { name: '中控名称与地址' })).toBeVisible()
  await wizard.getByRole('button', { name: '跳过引导' }).click()
  await expect(page.getByRole('heading', { name: '我的项目' })).toBeVisible()
})

test('creating the first admin opens the wizard (English)', async ({ page }) => {
  await page.goto('/?demo=1&lang=en#/login?setup=1')
  await page.getByLabel('Username').fill('first-admin')
  await page.getByLabel('Password', { exact: true }).fill('demo-password')
  await page.getByLabel('Confirm password').fill('demo-password')
  await page.getByRole('button', { name: 'Create and continue' }).click()
  const wizard = page.getByTestId('onboarding')
  await expect(wizard.getByRole('heading', { name: 'Welcome to Atoll' })).toBeVisible({ timeout: 20_000 })
  await expect(wizard.getByRole('heading', { name: 'Name and address' })).toBeVisible()
  await wizard.getByRole('button', { name: 'Skip the guide' }).click()
  await expect(page.getByRole('heading', { name: 'My projects' })).toBeVisible()
})
