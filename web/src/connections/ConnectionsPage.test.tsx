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

it('creates a GCP connection with a parsed write-only service account', async () => {
  const user = userEvent.setup()
  let submitted: Record<string, unknown> | undefined
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'gcp', name: 'Google Compute Engine' }] }), { status: 200 })
    if (url.endsWith('/connections') && init?.method === 'POST') {
      submitted = JSON.parse(String(init.body))
      return new Response(JSON.stringify({ connection: {
        id: 'gcp-a', name: '生产 GCP', provider_type: 'gcp', endpoint: '', settings: { project_id: 'example-project' },
        enabled: true, health_status: 'unknown', last_tested_at: null, last_synced_at: null,
        created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
      } }), { status: 201 })
    }
    return new Response(JSON.stringify({ connections: [] }), { status: 200 })
  })

  render(<ConnectionsPage />)
  await screen.findByText('还没有服务商连接，请先添加服务商。')
  await userEvent.click(screen.getByRole('button', { name: '添加服务商' }))
  await userEvent.type(screen.getByLabelText('连接名称'), '生产 GCP')
  await userEvent.type(screen.getByLabelText('GCP Project ID'), 'example-project')
  await user.click(screen.getByLabelText('Service Account JSON'))
  await user.paste(JSON.stringify({
    type: 'service_account',
    client_email: 'panel@example-project.iam.gserviceaccount.com',
    private_key: 'write-only-private-key',
    token_uri: 'https://oauth2.googleapis.com/token',
  }))
  await userEvent.click(screen.getByRole('button', { name: '保存并同步' }))

  await waitFor(() => expect(submitted).toMatchObject({
    name: '生产 GCP',
    provider_type: 'gcp',
    endpoint: '',
    settings: { project_id: 'example-project' },
    credentials: { service_account_json: {
      type: 'service_account',
      client_email: 'panel@example-project.iam.gserviceaccount.com',
      private_key: 'write-only-private-key',
      token_uri: 'https://oauth2.googleapis.com/token',
    } },
  }))
  expect(screen.queryByRole('form', { name: '添加服务商' })).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Service Account JSON')).not.toBeInTheDocument()
  expect(screen.queryByText('write-only-private-key')).not.toBeInTheDocument()
})

it('keeps the GCP form open and does not submit malformed service account JSON', async () => {
  const user = userEvent.setup()
  let postCount = 0
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'gcp', name: 'Google Compute Engine' }] }), { status: 200 })
    if (url.endsWith('/connections') && init?.method === 'POST') postCount += 1
    return new Response(JSON.stringify({ connections: [] }), { status: 200 })
  })

  render(<ConnectionsPage />)
  await screen.findByText('还没有服务商连接，请先添加服务商。')
  await userEvent.click(screen.getByRole('button', { name: '添加服务商' }))
  await userEvent.type(screen.getByLabelText('连接名称'), '无效 GCP')
  await userEvent.type(screen.getByLabelText('GCP Project ID'), 'example-project')
  await user.click(screen.getByLabelText('Service Account JSON'))
  await user.paste('{invalid')
  await userEvent.click(screen.getByRole('button', { name: '保存并同步' }))

  expect(await screen.findByRole('alert')).toHaveTextContent('Service Account JSON 格式无效')
  expect(screen.getByRole('form', { name: '添加服务商' })).toBeInTheDocument()
  expect(postCount).toBe(0)
})

it('creates a Virtualizor connection with write-only API credentials', async () => {
  let submitted: Record<string, unknown> | undefined
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'virtualizor', name: 'Virtualizor' }] }), { status: 200 })
    if (url.endsWith('/connections') && init?.method === 'POST') {
      submitted = JSON.parse(String(init.body))
      return new Response(JSON.stringify({ connection: {
        id: 'virtualizor-a', name: 'Virtualizor 主节点', provider_type: 'virtualizor', endpoint: 'https://panel.example.test:4083', settings: {},
        enabled: true, health_status: 'unknown', last_tested_at: null, last_synced_at: null,
        created_at: '2026-09-18T12:00:00Z', updated_at: '2026-09-18T12:00:00Z',
      } }), { status: 201 })
    }
    return new Response(JSON.stringify({ connections: [] }), { status: 200 })
  })

  render(<ConnectionsPage />)
  await screen.findByText('还没有服务商连接，请先添加服务商。')
  await userEvent.click(screen.getByRole('button', { name: '添加服务商' }))
  await userEvent.type(screen.getByLabelText('连接名称'), 'Virtualizor 主节点')
  await userEvent.type(screen.getByLabelText('Virtualizor 面板地址'), 'https://panel.example.test:4083')
  await userEvent.type(screen.getByLabelText('Virtualizor API Key'), 'write-only-api-key')
  await userEvent.type(screen.getByLabelText('Virtualizor API Password'), 'write-only-api-password')
  await userEvent.click(screen.getByRole('button', { name: '保存并同步' }))

  await waitFor(() => expect(submitted).toMatchObject({
    name: 'Virtualizor 主节点', provider_type: 'virtualizor', endpoint: 'https://panel.example.test:4083',
    settings: {}, credentials: { api_key: 'write-only-api-key', api_password: 'write-only-api-password' },
  }))
  expect(screen.queryByRole('form', { name: '添加服务商' })).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Virtualizor API Key')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Virtualizor API Password')).not.toBeInTheDocument()
  expect(screen.queryByText('write-only-api-key')).not.toBeInTheDocument()
  expect(screen.queryByText('write-only-api-password')).not.toBeInTheDocument()
})

it('creates a SolusVM 2 connection with a write-only API token', async () => {
  let submitted: Record<string, unknown> | undefined
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    if (url.endsWith('/provider-types')) return new Response(JSON.stringify({ provider_types: [{ id: 'solusvm2', name: 'SolusVM 2' }] }), { status: 200 })
    if (url.endsWith('/connections') && init?.method === 'POST') {
      submitted = JSON.parse(String(init.body))
      return new Response(JSON.stringify({ connection: {
        id: 'solusvm2-a', name: 'SolusVM 2 主节点', provider_type: 'solusvm2', endpoint: 'https://panel.example.test', settings: {},
        enabled: true, health_status: 'unknown', last_tested_at: null, last_synced_at: null,
        created_at: '2026-09-21T12:00:00Z', updated_at: '2026-09-21T12:00:00Z',
      } }), { status: 201 })
    }
    return new Response(JSON.stringify({ connections: [] }), { status: 200 })
  })

  render(<ConnectionsPage />)
  await screen.findByText('还没有服务商连接，请先添加服务商。')
  await userEvent.click(screen.getByRole('button', { name: '添加服务商' }))
  await userEvent.type(screen.getByLabelText('连接名称'), 'SolusVM 2 主节点')
  await userEvent.type(screen.getByLabelText('SolusVM 2 面板地址'), '  https://panel.example.test  ')
  const token = screen.getByLabelText('SolusVM 2 API Token')
  expect(token).toHaveAttribute('type', 'password')
  expect(token).toHaveAttribute('autocomplete', 'new-password')
  await userEvent.type(token, 'write-only-solusvm2-token')
  expect(screen.getByText('面板必须使用 HTTPS；私网地址需由管理员在服务端白名单中放行。')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '保存并同步' }))

  await waitFor(() => expect(submitted).toEqual({
    name: 'SolusVM 2 主节点', provider_type: 'solusvm2', endpoint: 'https://panel.example.test', enabled: true,
    settings: {}, credentials: { api_token: 'write-only-solusvm2-token' },
  }))
  expect(screen.queryByRole('form', { name: '添加服务商' })).not.toBeInTheDocument()
  expect(screen.queryByLabelText('SolusVM 2 API Token')).not.toBeInTheDocument()
  expect(screen.queryByText('write-only-solusvm2-token')).not.toBeInTheDocument()
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
