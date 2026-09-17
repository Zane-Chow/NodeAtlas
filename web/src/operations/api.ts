import { apiRequest } from '../api/client'
import type { Operation, PowerAction } from './types'

export async function requestPowerAction(serverID: string, action: PowerAction, idempotencyKey: string): Promise<Operation> {
  const response = await apiRequest<{ operation: Operation }>(`/servers/${encodeURIComponent(serverID)}/actions/${action}`, {
    method: 'POST',
    headers: { 'Idempotency-Key': idempotencyKey },
  })
  return response.operation
}

export async function listOperations(): Promise<Operation[]> {
  return (await apiRequest<{ operations: Operation[]; total: number }>('/operations')).operations
}
