import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { ConnectionsPage } from './ConnectionsPage'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('creates another Mock connection without rendering its credential', async () => {
  const requests: Array<{ url: string; init?: RequestInit }> = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    requests.push({ url, init })
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'mock', name: 'Mock Provider' }] }), { status: 200 })
    if (url.endsWith('/connections') && init?.method === 'POST') {
      return new Response(JSON.stringify({ connection: {
        id: 'connection-b', name: '实验室 B', provider_type: 'mock', endpoint: '', settings: { server_count: 3 },
        enabled: true, health_status: 'unknown', last_tested_at: null, last_synced_at: null,
        created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
      } }), { status: 201 })
    }
    return new Response(JSON.stringify({ connections: [{
      id: 'connection-a', name: '实验室 A', provider_type: 'mock', endpoint: '', settings: { server_count: 2 },
      enabled: true, health_status: 'healthy', last_tested_at: null, last_synced_at: '2026-09-18T11:00:00Z',
      created_at: '2026-09-18T10:00:00Z', updated_at: '2026-09-18T11:00:00Z',
    }] }), { status: 200 })
  })

  render(<ConnectionsPage />)
  expect(await screen.findByText('实验室 A')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '添加服务商' }))
  await userEvent.type(screen.getByLabelText('连接名称'), '实验室 B')
  await userEvent.clear(screen.getByLabelText('服务器数量'))
  await userEvent.type(screen.getByLabelText('服务器数量'), '3')
  await userEvent.type(screen.getByLabelText('Mock Token'), 'write-only-token')
  await userEvent.click(screen.getByRole('button', { name: '保存并同步' }))

  expect(await screen.findByText('实验室 B')).toBeInTheDocument()
  expect(screen.queryByText('write-only-token')).not.toBeInTheDocument()
  await waitFor(() => expect(requests.some(({ init }) => String(init?.body).includes('write-only-token'))).toBe(true))
})

it('creates an AWS connection with multiple regions and write-only static credentials', async () => {
  let submitted: Record<string, unknown> | undefined
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'aws', name: 'AWS EC2' }, { id: 'mock', name: 'Mock Provider' }] }), { status: 200 })
    if (url.endsWith('/connections') && init?.method === 'POST') {
      submitted = JSON.parse(String(init.body))
      return new Response(JSON.stringify({ connection: {
        id: 'aws-a', name: '生产 AWS', provider_type: 'aws', endpoint: '', settings: { regions: ['us-east-1', 'eu-west-1'] },
        enabled: true, health_status: 'unknown', last_tested_at: null, last_synced_at: null,
        created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
      } }), { status: 201 })
    }
    return new Response(JSON.stringify({ connections: [] }), { status: 200 })
  })

  render(<ConnectionsPage />)
  await screen.findByText('还没有服务商连接，请先添加服务商。')
  await userEvent.click(screen.getByRole('button', { name: '添加服务商' }))
  await userEvent.selectOptions(screen.getByLabelText('服务商类型'), 'aws')
  await userEvent.type(screen.getByLabelText('连接名称'), '生产 AWS')
  await userEvent.type(screen.getByLabelText('AWS Regions'), 'us-east-1, eu-west-1')
  await userEvent.type(screen.getByLabelText('Access Key ID'), 'AKIATEST')
  await userEvent.type(screen.getByLabelText('Secret Access Key'), 'write-only-secret')
  await userEvent.type(screen.getByLabelText('Session Token（可选）'), 'write-only-session')
  await userEvent.click(screen.getByRole('button', { name: '保存并同步' }))

  await waitFor(() => expect(submitted).toMatchObject({
    name: '生产 AWS', provider_type: 'aws', settings: { regions: ['us-east-1', 'eu-west-1'] },
    credentials: { access_key_id: 'AKIATEST', secret_access_key: 'write-only-secret', session_token: 'write-only-session' },
  }))
  expect(screen.queryByText('write-only-secret')).not.toBeInTheDocument()
})

it('creates a VirtFusion connection with a write-only bearer token', async () => {
  let submitted: Record<string, unknown> | undefined
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'virtfusion', name: 'VirtFusion' }, { id: 'mock', name: 'Mock Provider' }] }), { status: 200 })
    if (url.endsWith('/connections') && init?.method === 'POST') {
      submitted = JSON.parse(String(init.body))
      return new Response(JSON.stringify({ connection: {
        id: 'vf-a', name: 'VF 欧洲节点', provider_type: 'virtfusion', endpoint: 'https://vf.example.test', settings: { page_size: 100 },
        enabled: true, health_status: 'unknown', last_tested_at: null, last_synced_at: null,
        created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
      } }), { status: 201 })
    }
    return new Response(JSON.stringify({ connections: [] }), { status: 200 })
  })

  render(<ConnectionsPage />)
  await screen.findByText('还没有服务商连接，请先添加服务商。')
  await userEvent.click(screen.getByRole('button', { name: '添加服务商' }))
  await userEvent.selectOptions(screen.getByLabelText('服务商类型'), 'virtfusion')
  await userEvent.type(screen.getByLabelText('连接名称'), 'VF 欧洲节点')
  await userEvent.type(screen.getByLabelText('VirtFusion 面板地址'), 'https://vf.example.test')
  await userEvent.clear(screen.getByLabelText('每页服务器数'))
  await userEvent.type(screen.getByLabelText('每页服务器数'), '100')
  await userEvent.type(screen.getByLabelText('API Bearer Token'), 'write-only-vf-token')
  await userEvent.click(screen.getByRole('button', { name: '保存并同步' }))

  await waitFor(() => expect(submitted).toMatchObject({
    name: 'VF 欧洲节点', provider_type: 'virtfusion', endpoint: 'https://vf.example.test',
    settings: { page_size: 100 }, credentials: { token: 'write-only-vf-token' },
  }))
  expect(screen.queryByText('write-only-vf-token')).not.toBeInTheDocument()
})

it('tests and synchronizes a connection', async () => {
  const calls: string[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    calls.push(`${init?.method ?? 'GET'} ${url}`)
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'mock', name: 'Mock Provider' }] }), { status: 200 })
    if (url.endsWith('/test')) return new Response(JSON.stringify({ healthy: true, message: 'Connection succeeded' }), { status: 200 })
    if (url.endsWith('/sync')) return new Response(JSON.stringify({ status: 'queued' }), { status: 202 })
    return new Response(JSON.stringify({ connections: [{
      id: 'connection-a', name: '实验室 A', provider_type: 'mock', endpoint: '', settings: {}, enabled: true,
      health_status: 'unknown', last_tested_at: null, last_synced_at: null,
      created_at: '2026-09-18T10:00:00Z', updated_at: '2026-09-18T10:00:00Z',
    }] }), { status: 200 })
  })
  render(<ConnectionsPage />)
  expect(await screen.findByText('实验室 A')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '测试 实验室 A' }))
  expect(await screen.findByText('连接测试成功')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '同步 实验室 A' }))
  expect(await screen.findByText('同步任务已排队')).toBeInTheDocument()
  expect(calls).toContain('POST /api/v1/connections/connection-a/test')
  expect(calls).toContain('POST /api/v1/connections/connection-a/sync')
})

it('requires confirmation before deleting a connection', async () => {
  const calls: string[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    calls.push(`${init?.method ?? 'GET'} ${url}`)
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'mock', name: 'Mock Provider' }] }), { status: 200 })
    if (init?.method === 'DELETE') return new Response(null, { status: 204 })
    return new Response(JSON.stringify({ connections: [{
      id: 'connection-a', name: '待删除连接', provider_type: 'mock', endpoint: '', settings: {}, enabled: false,
      health_status: 'offline', last_tested_at: null, last_synced_at: null,
      created_at: '2026-09-18T10:00:00Z', updated_at: '2026-09-18T10:00:00Z',
    }] }), { status: 200 })
  })
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  render(<ConnectionsPage />)
  expect(await screen.findByText('待删除连接')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '同步 待删除连接' })).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: '删除 待删除连接' }))
  expect(window.confirm).toHaveBeenCalledWith('确定删除服务商连接“待删除连接”吗？同步到本地的服务器也会从清单中移除。')
  expect(calls).toContain('DELETE /api/v1/connections/connection-a')
  expect(screen.queryByText('待删除连接')).not.toBeInTheDocument()
})
