import { expect, test } from '@playwright/test'

test('demo login, create a service, bind a domain, and switch languages', async ({ page }) => {
  await page.goto('/?demo=1#/login')
  await page.getByLabel('用户名').fill('e2e-admin')
  await page.getByLabel('密码').fill('demo-password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '我的项目' })).toBeVisible()

  await page.getByTestId('project-card').filter({ hasText: 'sandbox' }).click()
  await page.getByRole('button', { name: '新建服务', exact: true }).click()
  await page.getByRole('button', { name: /Docker 容器镜像/ }).click()
  await page.getByPlaceholder('ghcr.io/owner/image:tag').fill('nginx:1-alpine')
  await page.getByPlaceholder('nginx').fill('e2e-web')
  await page.getByRole('button', { name: '部署', exact: true }).click()

  await page.getByRole('button', { name: '绑定域名', exact: true }).click()
  await page.getByPlaceholder(/前缀，如 app/).fill('e2e-ui')
  await page.getByRole('button', { name: '绑定', exact: true }).click()
  await expect(page.getByRole('link', { name: /e2e-ui\.example\.com/ })).toBeVisible()

  await page.getByRole('button', { name: 'Switch to English' }).click()
  await expect(page.getByRole('link', { name: 'Projects', exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Projects', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'My projects' })).toBeVisible()
  await page.getByRole('button', { name: '切换到中文' }).click()
  await expect(page.getByRole('heading', { name: '我的项目' })).toBeVisible()
})

test('read-only demo sub-account cannot see admin navigation or deployment actions', async ({ page }) => {
  await page.goto('/?demo=1&member=ro#/')
  await expect(page.getByText('alice', { exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: '项目', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: '设置', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '创建项目', exact: true })).toHaveCount(0)

  await page.getByTestId('project-card').first().click()
  await expect(page.getByRole('button', { name: '新建服务', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '停止', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '启动', exact: true })).toHaveCount(0)
})
