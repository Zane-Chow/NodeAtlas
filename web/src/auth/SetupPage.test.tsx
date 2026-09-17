import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { SetupPage } from './SetupPage'

it('requires matching passwords before initializing', async () => {
  const initialize = vi.fn().mockResolvedValue(undefined)
  render(<SetupPage onInitialize={initialize} />)
  await userEvent.type(screen.getByLabelText('管理员用户名'), 'admin')
  await userEvent.type(screen.getByLabelText('密码', { selector: '#setup-password' }), 'secure passphrase')
  await userEvent.type(screen.getByLabelText('确认密码'), 'different passphrase')
  await userEvent.click(screen.getByRole('button', { name: '创建管理员' }))
  expect(screen.getByRole('alert')).toHaveTextContent('两次输入的密码不一致')
  expect(initialize).not.toHaveBeenCalled()
})
