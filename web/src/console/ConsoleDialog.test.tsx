import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { ConsoleDialog } from './ConsoleDialog'

const fakeRFB = vi.hoisted(() => ({ instances: [] as Array<{
  target: Element
  url: string
  options: { credentials?: { password?: string } }
  client: EventTarget & { scaleViewport: boolean; resizeSession: boolean; clipViewport: boolean; focusOnClick: boolean; disconnect: ReturnType<typeof vi.fn> }
}> }))

vi.mock('@novnc/novnc', () => ({ default: class extends EventTarget {
  scaleViewport = false
  resizeSession = false
  clipViewport = false
  focusOnClick = false
  disconnect = vi.fn()
  sendCredentials = vi.fn()
  constructor(target: Element, url: string, options: { credentials?: { password?: string } }) {
    super()
    fakeRFB.instances.push({ target, url, options, client: this })
  }
} }))

afterEach(() => { cleanup(); fakeRFB.instances.length = 0 })

it('uses noVNC for an RFB session without exposing the upstream websocket URL', async () => {
  const rendered = render(<ConsoleDialog serverName="VF node" session={{
    session_id: 'session-vnc', ticket: 'local-ticket', expires_at: '2026-09-18T12:01:00Z',
    protocol: 'rfb', credentials: { password: 'temporary-password' },
  }} onClose={() => undefined} />)

  await waitFor(() => expect(fakeRFB.instances).toHaveLength(1))
  const instance = fakeRFB.instances[0]
  expect(instance.url).toContain('/ws/console/local-ticket')
  expect(instance.url).not.toContain('virtfusion')
  expect(instance.options.credentials?.password).toBe('temporary-password')
  expect(instance.client.scaleViewport).toBe(true)
  expect(instance.client.resizeSession).toBe(true)
  instance.client.dispatchEvent(new Event('connect'))
  expect(await screen.findByText('已连接')).toBeInTheDocument()

  instance.client.dispatchEvent(new Event('securityfailure'))
  expect(await screen.findByText('VNC 验证失败')).toBeInTheDocument()

  rendered.unmount()
  expect(instance.client.disconnect).toHaveBeenCalled()
})
