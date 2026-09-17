import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { AuthProvider } from './AuthContext'
import { useAuth } from './useAuth'

function Harness() {
  const auth = useAuth()
  return (
    <div>
      <output>{auth.state.status}</output>
      <button onClick={() => void auth.login('admin', 'correct horse battery staple')}>Login</button>
    </div>
  )
}

afterEach(() => vi.restoreAllMocks())

it('discovers initialized state and transitions after login', async () => {
  const responses = [
    new Response(JSON.stringify({ requires_setup: false }), { status: 200 }),
    new Response(JSON.stringify({ error: { code: 'unauthenticated', message: 'Login required', request_id: '1' } }), { status: 401 }),
    new Response(JSON.stringify({ user: { id: 'user-1', username: 'Admin' } }), { status: 200 }),
  ]
  vi.spyOn(globalThis, 'fetch').mockImplementation(async () => responses.shift()!)

  render(<AuthProvider><Harness /></AuthProvider>)
  await waitFor(() => expect(screen.getByText('unauthenticated')).toBeInTheDocument())
  await userEvent.click(screen.getByRole('button', { name: 'Login' }))
  await waitFor(() => expect(screen.getByText('authenticated')).toBeInTheDocument())
})

it('enters setup state when initialization is required', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ requires_setup: true }), { status: 200 }),
  )

  render(<AuthProvider><Harness /></AuthProvider>)

  await waitFor(() => expect(screen.getByText('setup-required')).toBeInTheDocument())
})
