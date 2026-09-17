import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { APIRequestError, apiRequest, readCSRFCookie, setCSRFToken } from '../api/client'
import { AuthContext, type AuthContextValue, type AuthState, type AuthUser } from './useAuth'

type UserResponse = { user: AuthUser }

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: 'loading' })

  useEffect(() => {
    let active = true
    async function bootstrap() {
      try {
        const setup = await apiRequest<{ requires_setup: boolean }>('/setup/status')
        if (!active) return
        if (setup.requires_setup) {
          setState({ status: 'setup-required' })
          return
        }
        try {
          const response = await apiRequest<UserResponse>('/auth/me')
          if (active) {
            setCSRFToken(readCSRFCookie())
            setState({ status: 'authenticated', user: response.user })
          }
        } catch (error) {
          if (active && error instanceof APIRequestError && error.detail.code === 'unauthenticated') {
            setState({ status: 'unauthenticated' })
            return
          }
          throw error
        }
      } catch {
        if (active) setState({ status: 'unauthenticated' })
      }
    }
    void bootstrap()
    return () => { active = false }
  }, [])

  const value = useMemo<AuthContextValue>(() => ({
    state,
    async initialize(username, password) {
      const response = await apiRequest<UserResponse>('/setup/initialize', {
        method: 'POST', body: JSON.stringify({ username, password }),
      })
      setCSRFToken(readCSRFCookie())
      setState({ status: 'authenticated', user: response.user })
    },
    async login(username, password) {
      const response = await apiRequest<UserResponse>('/auth/login', {
        method: 'POST', body: JSON.stringify({ username, password }),
      })
      setCSRFToken(readCSRFCookie())
      setState({ status: 'authenticated', user: response.user })
    },
    async logout() {
      await apiRequest<void>('/auth/logout', { method: 'POST' })
      setCSRFToken(null)
      setState({ status: 'unauthenticated' })
    },
  }), [state])

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
