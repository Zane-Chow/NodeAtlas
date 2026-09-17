import { apiRequest } from '../api/client'
import type { ProviderConnection, ProviderType } from './types'

export async function listProviderTypes(): Promise<ProviderType[]> {
  return (await apiRequest<{ provider_types: ProviderType[] }>('/provider-types')).provider_types
}

export async function listConnections(): Promise<ProviderConnection[]> {
  return (await apiRequest<{ connections: ProviderConnection[] }>('/connections')).connections
}

export async function createConnection(input: {
  name: string
  provider_type: string
  endpoint: string
  settings: Record<string, unknown>
  credentials: Record<string, unknown>
  enabled: boolean
}): Promise<ProviderConnection> {
  return (await apiRequest<{ connection: ProviderConnection }>('/connections', {
    method: 'POST', body: JSON.stringify(input),
  })).connection
}

export async function testConnection(id: string): Promise<{ healthy: boolean; message: string }> {
  return await apiRequest(`/connections/${id}/test`, { method: 'POST', body: '{}' })
}

export async function syncConnection(id: string): Promise<void> {
  await apiRequest(`/connections/${id}/sync`, { method: 'POST', body: '{}' })
}

export async function deleteConnection(id: string): Promise<void> {
  await apiRequest(`/connections/${id}`, { method: 'DELETE' })
}
