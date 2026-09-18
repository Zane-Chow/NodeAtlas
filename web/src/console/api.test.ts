import { afterEach, expect, it, vi } from 'vitest'
import { openConsoleWindow, openEmbeddedConsoleWindow, openProviderPortal } from './api'

afterEach(() => vi.restoreAllMocks())

it('requests validated external targets and opens them without an opener', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => new Response(JSON.stringify({ target: {
    url: String(input).includes('provider-portal') ? 'https://provider.example.test/server' : 'https://console.example.test/session',
  } }), { status: 200 }))
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  await openConsoleWindow('server-a')
  await openProviderPortal('server-a')
  expect(open).toHaveBeenNthCalledWith(1, 'https://console.example.test/session', '_blank', 'noopener,noreferrer')
  expect(open).toHaveBeenNthCalledWith(2, 'https://provider.example.test/server', '_blank', 'noopener,noreferrer')
})

it('hands an RFB session to a same-origin popout without putting secrets in its URL', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ session: {
    session_id: 'session-a', ticket: 'one-use-ticket', expires_at: '2026-09-18T12:01:00Z',
    protocol: 'rfb', credentials: { password: 'temporary-password' },
  } }), { status: 201 }))
  const popup = { postMessage: vi.fn(), close: vi.fn() } as unknown as Window
  const open = vi.spyOn(window, 'open').mockReturnValue(popup)

  const opened = openEmbeddedConsoleWindow('server-a', 'VF node')
  window.dispatchEvent(new MessageEvent('message', {
    origin: window.location.origin, source: popup, data: { type: 'server-control:console-ready' },
  }))

  await expect(opened).resolves.toBe(true)
  expect(open).toHaveBeenCalledWith('/console-popout', '_blank')
  expect(String(open.mock.calls[0])).not.toContain('one-use-ticket')
  expect(String(open.mock.calls[0])).not.toContain('temporary-password')
  expect(popup.postMessage).toHaveBeenCalledWith(expect.objectContaining({
    type: 'server-control:console-session', serverName: 'VF node',
  }), window.location.origin)
})
