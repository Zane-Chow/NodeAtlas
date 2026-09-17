import { apiRequest } from '../api/client'
import type { Server } from './types'
export async function listServers(): Promise<Server[]> {
  return (await apiRequest<{ servers: Server[]; total: number }>('/servers')).servers
}
export async function refreshServer(id: string): Promise<void> {
  await apiRequest(`/servers/${id}/refresh`, { method: 'POST' })
}
