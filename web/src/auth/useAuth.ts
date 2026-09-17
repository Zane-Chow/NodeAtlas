import { createContext, useContext } from 'react'

export type AuthUser = { id: string; username: string }

export type AuthState =
  | { status: 'loading' }
  | { status: 'setup-required' }
  | { status: 'unauthenticated' }
  | { status: 'authenticated'; user: AuthUser }

export type AuthContextValue = {
  state: AuthState
  initialize(username: string, password: string): Promise<void>
  login(username: string, password: string): Promise<void>
  logout(): Promise<void>
}

export const AuthContext = createContext<AuthContextValue | null>(null)

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth must be used inside AuthProvider')
  return value
}
