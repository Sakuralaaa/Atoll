import { expect, test } from '@playwright/test'

test('with 2FA on, changing the password asks for the code in a dialog', async ({ page }) => {
  await page.goto('/?demo=1#/settings/security#login')
  await page.getByRole('button', { name: '启用两步验证' }).click()
  await page.getByPlaceholder('当前密码').fill('demo-pass')
  await page.getByRole('button', { name: '下一步' }).click()
  await page.getByPlaceholder('000000').fill('123456')
  await page.getByRole('button', { name: '验证并启用' }).click()
  await page.getByRole('button', { name: '下载 .txt' }).click()
  await page.getByRole('button', { name: '我已保存好' }).click()

  const card = page.locator('form').filter({ has: page.getByText('确认新密码') })
  await expect(card.getByPlaceholder('000000')).toHaveCount(0) // no inline code field
  await card.locator('input[autocomplete="current-password"]').fill('old-password')
  await card.locator('input[autocomplete="new-password"]').nth(0).fill('new-password-1')
  await card.locator('input[autocomplete="new-password"]').nth(1).fill('new-password-1')
  await card.getByRole('button', { name: '修改密码' }).click()

  const dlg = page.getByRole('dialog')
  await expect(dlg.getByText('两步验证码')).toBeVisible()
  await expect(dlg.getByRole('button', { name: '验证' })).toBeDisabled()
  await dlg.getByPlaceholder('000000').fill('654321')
  await dlg.getByRole('button', { name: '验证' }).click()
  await expect(dlg).toHaveCount(0)
  await expect(page.getByText('密码已修改，其他设备已退出')).toBeVisible()
})
