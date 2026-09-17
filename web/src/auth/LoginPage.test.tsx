import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { LoginPage } from './LoginPage'

it('shows a generic login error', async () => {
  const login = vi.fn().mockRejectedValue(new Error('details must stay hidden'))
  render(<LoginPage onLogin={login} />)
  await userEvent.type(screen.getByLabelText('用户名'), 'admin')
  await userEvent.type(screen.getByLabelText('密码'), 'wrong password')
  await userEvent.click(screen.getByRole('button', { name: '登录' }))
  expect(screen.getByRole('alert')).toHaveTextContent('用户名或密码错误')
})
