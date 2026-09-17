import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth/AuthContext'
import { App } from './App'

afterEach(() => vi.restoreAllMocks())

it('renders the operator navigation for an authenticated administrator', async () => {
  const responses = [
    new Response(JSON.stringify({ requires_setup: false }), { status: 200 }),
    new Response(JSON.stringify({ user: { id: 'user-1', username: 'Admin' } }), { status: 200 }),
  ]
  vi.spyOn(globalThis, 'fetch').mockImplementation(async () => responses.shift()!)

  render(<AuthProvider><App /></AuthProvider>)

  await waitFor(() => expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument())
  for (const label of ['总览', '服务器', '服务商', '操作记录', '备份与设置']) {
    expect(screen.getByText(label)).toBeInTheDocument()
  }
})
