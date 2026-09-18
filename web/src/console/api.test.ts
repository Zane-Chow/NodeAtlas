import { afterEach, expect, it, vi } from 'vitest'
import { openConsoleWindow, openProviderPortal } from './api'

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
