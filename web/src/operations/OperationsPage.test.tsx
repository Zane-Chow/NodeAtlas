import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { OperationsPage } from './OperationsPage'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('renders newest operation history with target and status labels', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    if (String(input).endsWith('/servers')) return new Response(JSON.stringify({ servers: [
      { id: 'server-a', name: 'web-01' }, { id: 'server-b', name: 'db-01' },
    ], total: 2 }), { status: 200 })
    return new Response(JSON.stringify({ operations: [
      { id: 'operation-b', server_id: 'server-b', connection_id: 'connection-a', action: 'reboot', status: 'failed', error_message: 'Provider unavailable', queued_at: '2026-09-18T12:01:00Z', started_at: '2026-09-18T12:01:01Z', finished_at: '2026-09-18T12:01:02Z', updated_at: '2026-09-18T12:01:02Z' },
      { id: 'operation-a', server_id: 'server-a', connection_id: 'connection-a', action: 'start', status: 'succeeded', queued_at: '2026-09-18T12:00:00Z', started_at: '2026-09-18T12:00:01Z', finished_at: '2026-09-18T12:00:02Z', updated_at: '2026-09-18T12:00:02Z' },
    ], total: 2 }), { status: 200 })
  })
  render(<OperationsPage />)
  expect(await screen.findByText('db-01')).toBeInTheDocument()
  expect(screen.getByText('重启')).toBeInTheDocument()
  expect(screen.getByText('失败')).toBeInTheDocument()
  expect(screen.getByText('Provider unavailable')).toBeInTheDocument()
  expect(screen.getByText('web-01')).toBeInTheDocument()
  expect(screen.getByText('成功')).toBeInTheDocument()
})

it('shows an empty operation history', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => String(input).endsWith('/servers')
    ? new Response(JSON.stringify({ servers: [], total: 0 }), { status: 200 })
    : new Response(JSON.stringify({ operations: [], total: 0 }), { status: 200 }))
  render(<OperationsPage />)
  expect(await screen.findByText('还没有电源操作记录')).toBeInTheDocument()
})
