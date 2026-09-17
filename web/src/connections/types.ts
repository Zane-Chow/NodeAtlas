export type ProviderType = { id: string; name: string }

export type ProviderConnection = {
  id: string
  name: string
  provider_type: string
  endpoint: string
  settings: Record<string, unknown>
  enabled: boolean
  health_status: 'unknown' | 'healthy' | 'degraded' | 'offline'
  last_tested_at: string | null
  last_synced_at: string | null
  last_error_code?: string
  last_error_message?: string
  created_at: string
  updated_at: string
}
