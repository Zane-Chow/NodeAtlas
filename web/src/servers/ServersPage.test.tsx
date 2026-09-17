import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { ServersPage } from './ServersPage'

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.history.replaceState({}, '', '/') })

it('filters the unified server list and opens details', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (String(input).endsWith('/connections')) return new Response(JSON.stringify({ connections: [{ id: 'connection-a', name: '实验室 A', provider_type: 'mock', enabled: true }] }), { status: 200 })
    return new Response(JSON.stringify({ servers: [
    {
      id: 'server-a', connection_id: 'connection-a', external_id: 'vm-a', scope: 'zone-a', name: 'api-01',
      state: 'running', remote_state: 'RUNNING', spec: { cpu: 2, memory_mb: 2048 }, addresses: [{ type: 'private', address: '10.0.0.1' }],
      capabilities: { can_start: { available: false }, can_stop: { available: true }, can_reboot: { available: true }, can_embed_console: { available: true }, can_open_console_window: { available: true }, has_provider_portal: { available: true } },
      last_seen_at: '2026-09-18T12:00:00Z', last_state_checked_at: '2026-09-18T12:00:00Z', created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
    },
    {
      id: 'server-b', connection_id: 'connection-a', external_id: 'vm-b', scope: 'zone-a', name: 'db-01',
      state: 'stopped', remote_state: 'STOPPED', spec: { cpu: 4, memory_mb: 4096 }, addresses: [], capabilities: {},
      last_seen_at: '2026-09-18T12:00:00Z', last_state_checked_at: '2026-09-18T12:00:00Z', created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
    },
    ], total: 2 }), { status: 200 })
  })

  render(<ServersPage />)
  expect(await screen.findByText('api-01')).toBeInTheDocument()
  expect(screen.getByText('db-01')).toBeInTheDocument()
  await userEvent.selectOptions(screen.getByLabelText('运行状态'), 'running')
  expect(screen.getByText('api-01')).toBeInTheDocument()
  expect(screen.queryByText('db-01')).not.toBeInTheDocument()
  await userEvent.type(screen.getByLabelText('搜索服务器'), 'api')
  await userEvent.click(screen.getByRole('button', { name: '查看 api-01' }))
  const detail = screen.getByRole('dialog', { name: '服务器详情' })
  expect(detail).toBeInTheDocument()
  expect(within(detail).getByText('2 vCPU')).toBeInTheDocument()
  expect(within(detail).getByText('10.0.0.1')).toBeInTheDocument()
  expect(within(detail).getByRole('button', { name: '开机 · 里程碑 3' })).toBeDisabled()
  expect(within(detail).getByRole('button', { name: '控制台 · 里程碑 4' })).toBeDisabled()
})

it('filters by provider connection and shows capability and synchronization details', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (String(input).endsWith('/connections')) return new Response(JSON.stringify({ connections: [
      { id: 'connection-a', name: '东京 AWS', provider_type: 'aws', enabled: true },
      { id: 'connection-b', name: '欧洲 VirtFusion', provider_type: 'virtfusion', enabled: true },
    ] }), { status: 200 })
    return new Response(JSON.stringify({ servers: [
      { id: 'server-a', connection_id: 'connection-a', external_id: 'vm-a', scope: 'ap-northeast-1', name: 'api-aws', state: 'running', remote_state: 'RUNNING', spec: {}, addresses: [], capabilities: { can_stop: { available: true }, can_embed_console: { available: true } }, last_seen_at: '2026-09-18T12:00:00Z', last_state_checked_at: '2026-09-18T12:00:00Z', created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z' },
      { id: 'server-b', connection_id: 'connection-b', external_id: 'vm-b', scope: 'eu-1', name: 'web-vf', state: 'stopped', remote_state: 'STOPPED', spec: {}, addresses: [], capabilities: {}, last_seen_at: '2026-09-18T11:00:00Z', last_state_checked_at: '2026-09-18T11:00:00Z', created_at: '2026-09-18T11:00:00Z', updated_at: '2026-09-18T11:00:00Z' },
    ], total: 2 }), { status: 200 })
  })
  render(<ServersPage />)
  expect(await screen.findByText('api-aws')).toBeInTheDocument()
  await userEvent.selectOptions(screen.getByLabelText('服务商类型'), 'aws')
  expect(screen.getByText('api-aws')).toBeInTheDocument()
  expect(screen.queryByText('web-vf')).not.toBeInTheDocument()
  await userEvent.selectOptions(screen.getByLabelText('服务商连接'), 'connection-a')
  await userEvent.click(screen.getByRole('button', { name: '查看 api-aws' }))
  const detail = screen.getByRole('dialog', { name: '服务器详情' })
  expect(within(detail).getByText('东京 AWS')).toBeInTheDocument()
  expect(within(detail).getByText('可内嵌控制台')).toBeInTheDocument()
  expect(within(detail).getByText(/上次同步/)).toBeInTheDocument()
})

it('shows an empty inventory state', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => String(input).endsWith('/connections')
    ? new Response(JSON.stringify({ connections: [] }), { status: 200 })
    : new Response(JSON.stringify({ servers: [], total: 0 }), { status: 200 }))
  render(<ServersPage />)
  expect(await screen.findByText('还没有同步到服务器')).toBeInTheDocument()
})
