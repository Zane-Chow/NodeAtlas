import { apiRequest } from '../api/client'
import type { ConsoleSession, ExternalTarget } from './types'

export async function createEmbeddedConsole(serverID: string): Promise<ConsoleSession> {
  return (await apiRequest<{ session: ConsoleSession }>(`/servers/${encodeURIComponent(serverID)}/console-sessions`, { method: 'POST' })).session
}

export async function openConsoleWindow(serverID: string): Promise<boolean> {
  const target = (await apiRequest<{ target: ExternalTarget }>(`/servers/${encodeURIComponent(serverID)}/console-window`, { method: 'POST' })).target
  return window.open(target.url, '_blank', 'noopener,noreferrer') !== null
}

export async function openProviderPortal(serverID: string): Promise<boolean> {
  const target = (await apiRequest<{ target: ExternalTarget }>(`/servers/${encodeURIComponent(serverID)}/provider-portal`)).target
  return window.open(target.url, '_blank', 'noopener,noreferrer') !== null
}
