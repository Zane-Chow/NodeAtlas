import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiRequest, setCSRFToken } from './client'

describe('apiRequest', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    setCSRFToken(null)
  })

  it('includes credentials and sends CSRF only for mutations', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    setCSRFToken('csrf-value')

    await apiRequest('/status')
    await apiRequest('/change', { method: 'POST', body: JSON.stringify({ enabled: true }) })

    const getHeaders = new Headers(fetchMock.mock.calls[0][1]?.headers)
    const postHeaders = new Headers(fetchMock.mock.calls[1][1]?.headers)
    expect(fetchMock.mock.calls[0][1]?.credentials).toBe('include')
    expect(getHeaders.has('X-CSRF-Token')).toBe(false)
    expect(postHeaders.get('X-CSRF-Token')).toBe('csrf-value')
    expect(postHeaders.get('Content-Type')).toBe('application/json')
  })

  it('converts the stable error envelope into APIRequestError', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        error: { code: 'invalid_credentials', message: 'Login failed', request_id: 'request-1' },
      }), { status: 401, headers: { 'Content-Type': 'application/json' } }),
    )

    await expect(apiRequest('/auth/login', { method: 'POST' })).rejects.toMatchObject({
      name: 'APIRequestError',
      detail: { code: 'invalid_credentials', request_id: 'request-1' },
    })
  })
})
