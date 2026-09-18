import { apiRequest } from '../api/client'
import type { ConsoleSession, ExternalTarget } from './types'

export async function createEmbeddedConsole(serverID: string): Promise<ConsoleSession> {
  return (await apiRequest<{ session: ConsoleSession }>(`/servers/${encodeURIComponent(serverID)}/console-sessions`, { method: 'POST' })).session
}

export async function openConsoleWindow(serverID: string): Promise<boolean> {
  const target = (await apiRequest<{ target: ExternalTarget }>(`/servers/${encodeURIComponent(serverID)}/console-window`, { method: 'POST' })).target
  return window.open(target.url, '_blank', 'noopener,noreferrer') !== null
}

export async function openEmbeddedConsoleWindow(serverID: string, serverName: string): Promise<boolean> {
  const popup = window.open('/console-popout', '_blank')
  if (!popup) return false
  try {
    const ready = new Promise<void>((resolve, reject) => {
      const timeout = window.setTimeout(() => {
        window.removeEventListener('message', receiveReady)
        reject(new Error('Console window did not initialize'))
      }, 15_000)
      function receiveReady(event: MessageEvent) {
        if (event.origin !== window.location.origin || event.source !== popup || event.data?.type !== 'server-control:console-ready') return
        window.clearTimeout(timeout)
        window.removeEventListener('message', receiveReady)
        resolve()
      }
      window.addEventListener('message', receiveReady)
    })
    const [session] = await Promise.all([createEmbeddedConsole(serverID), ready])
    popup.postMessage({ type: 'server-control:console-session', serverName, session }, window.location.origin)
    return true
  } catch (error) {
    popup.close()
    throw error
  }
}

export async function openProviderPortal(serverID: string): Promise<boolean> {
  const target = (await apiRequest<{ target: ExternalTarget }>(`/servers/${encodeURIComponent(serverID)}/provider-portal`)).target
  return window.open(target.url, '_blank', 'noopener,noreferrer') !== null
}
