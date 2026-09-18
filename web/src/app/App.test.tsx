import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth/AuthContext'
import { App } from './App'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

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
  expect(screen.getByText('阶段二 · 真实服务商接入')).toBeInTheDocument()
  expect(screen.getByText('AWS 与 VirtFusion 已接入')).toBeInTheDocument()
})

it('navigates to provider connections and unified servers', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const url = String(input)
    if (url.endsWith('/setup/status')) return new Response(JSON.stringify({ requires_setup: false }), { status: 200 })
    if (url.endsWith('/auth/me')) return new Response(JSON.stringify({ user: { id: 'user-1', username: 'Admin' } }), { status: 200 })
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'mock', name: 'Mock Provider' }] }), { status: 200 })
    if (url.endsWith('/connections')) return new Response(JSON.stringify({ connections: [] }), { status: 200 })
    if (url.endsWith('/servers')) return new Response(JSON.stringify({ servers: [], total: 0 }), { status: 200 })
    if (url.endsWith('/operations')) return new Response(JSON.stringify({ operations: [], total: 0 }), { status: 200 })
    if (url.endsWith('/backups')) return new Response(JSON.stringify({ backups: [], total: 0 }), { status: 200 })
    return new Response(null, { status: 404 })
  })
  render(<AuthProvider><App /></AuthProvider>)
  await userEvent.click(await screen.findByRole('button', { name: '服务商' }))
  expect(await screen.findByRole('heading', { level: 2, name: '服务商连接' })).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '服务器' }))
  expect(await screen.findByRole('heading', { level: 2, name: '服务器' })).toBeInTheDocument()
  expect(await screen.findByText('还没有同步到服务器')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '操作记录' }))
  expect(await screen.findByRole('heading', { level: 2, name: '操作记录' })).toBeInTheDocument()
  expect(await screen.findByText('还没有电源操作记录')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '备份与设置' }))
  expect(await screen.findByRole('heading', { level: 2, name: '备份与设置' })).toBeInTheDocument()
  expect(await screen.findByText('还没有备份')).toBeInTheDocument()
})
