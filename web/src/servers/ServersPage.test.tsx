import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { ServersPage } from './ServersPage'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); window.history.replaceState({}, '', '/') })

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
  expect(within(detail).getByRole('button', { name: '开机' })).toBeDisabled()
  expect(within(detail).getByRole('button', { name: '关机' })).toBeEnabled()
  expect(within(detail).getByRole('button', { name: '重启' })).toBeEnabled()
  expect(within(detail).getByRole('button', { name: '内嵌控制台' })).toBeEnabled()
  expect(within(detail).getByRole('button', { name: '新窗口控制台' })).toBeEnabled()
  expect(within(detail).getByRole('button', { name: '服务商后台' })).toBeEnabled()
})

it('confirms and queues a supported power action', async () => {
  const calls: Array<{ url: string; init?: RequestInit }> = []
  vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue('11111111-1111-4111-8111-111111111111')
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    calls.push({ url, init })
    if (url.endsWith('/connections')) return new Response(JSON.stringify({ connections: [{ id: 'connection-a', name: '实验室 A', provider_type: 'mock', enabled: true }] }), { status: 200 })
    if (url.endsWith('/actions/start')) return new Response(JSON.stringify({ operation: { id: 'operation-a', server_id: 'server-a', action: 'start', status: 'queued' } }), { status: 202 })
    return new Response(JSON.stringify({ servers: [{
      id: 'server-a', connection_id: 'connection-a', external_id: 'vm-a', scope: 'zone-a', name: 'web-01',
      state: 'stopped', remote_state: 'STOPPED', spec: {}, addresses: [],
      capabilities: { can_start: { available: true }, can_stop: { available: false, reason: 'server must be running' }, can_reboot: { available: false, reason: 'server must be running' } },
      last_seen_at: '2026-09-18T12:00:00Z', last_state_checked_at: '2026-09-18T12:00:00Z', created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
    }], total: 1 }), { status: 200 })
  })
  render(<ServersPage />)
  await userEvent.click(await screen.findByRole('button', { name: '查看 web-01' }))
  const detail = screen.getByRole('dialog', { name: '服务器详情' })
  expect(within(detail).getByRole('button', { name: '关机' })).toBeDisabled()
  await userEvent.click(within(detail).getByRole('button', { name: '开机' }))
  const confirmation = screen.getByRole('dialog', { name: '确认开机' })
  await userEvent.click(within(confirmation).getByRole('button', { name: '确认开机' }))
  expect(await screen.findByText('开机操作已排队')).toBeInTheDocument()
  await waitFor(() => expect(calls.some(({ url, init }) => url.endsWith('/servers/server-a/actions/start')
    && init?.method === 'POST' && new Headers(init.headers).get('Idempotency-Key') === '11111111-1111-4111-8111-111111111111')).toBe(true))
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

it('creates an embedded one-use console and renders websocket output', async () => {
  class FakeWebSocket {
    static instance: FakeWebSocket
    static OPEN = 1
    readonly url: string
    readyState = 0
    onopen: (() => void) | null = null
    onmessage: ((event: MessageEvent) => void) | null = null
    onclose: (() => void) | null = null
    send = vi.fn()
    close = vi.fn()
    constructor(url: string) { this.url = url; FakeWebSocket.instance = this }
    emitOpen() { this.readyState = FakeWebSocket.OPEN; this.onopen?.() }
    emitMessage(data: string) { this.onmessage?.(new MessageEvent('message', { data })) }
  }
  vi.stubGlobal('WebSocket', FakeWebSocket)
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const url = String(input)
    if (url.endsWith('/connections')) return new Response(JSON.stringify({ connections: [{ id: 'connection-a', name: '实验室 A', provider_type: 'mock', enabled: true }] }), { status: 200 })
    if (url.endsWith('/console-sessions')) return new Response(JSON.stringify({ session: { session_id: 'session-a', ticket: 'ticket-a', expires_at: '2026-09-18T12:01:00Z' } }), { status: 201 })
    return new Response(JSON.stringify({ servers: [{
      id: 'server-a', connection_id: 'connection-a', external_id: 'vm-a', scope: 'zone-a', name: 'console-01',
      state: 'running', remote_state: 'RUNNING', spec: {}, addresses: [], capabilities: { can_embed_console: { available: true }, can_open_console_window: { available: false, reason: 'unavailable' }, has_provider_portal: { available: true } },
      last_seen_at: '2026-09-18T12:00:00Z', last_state_checked_at: '2026-09-18T12:00:00Z', created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
    }], total: 1 }), { status: 200 })
  })
  render(<ServersPage />)
  await userEvent.click(await screen.findByRole('button', { name: '查看 console-01' }))
  await userEvent.click(within(screen.getByRole('dialog', { name: '服务器详情' })).getByRole('button', { name: '内嵌控制台' }))
  const consoleDialog = await screen.findByRole('dialog', { name: 'console-01 控制台' })
  expect(FakeWebSocket.instance.url).toContain('/ws/console/ticket-a')
  FakeWebSocket.instance.emitOpen()
  FakeWebSocket.instance.emitMessage('Server Control Mock Console')
  expect(await within(consoleDialog).findByText(/Server Control Mock Console/)).toBeInTheDocument()
  await userEvent.type(within(consoleDialog).getByLabelText('控制台输入'), 'status')
  await userEvent.click(within(consoleDialog).getByRole('button', { name: '发送' }))
  expect(FakeWebSocket.instance.send).toHaveBeenCalledWith('status')
})
