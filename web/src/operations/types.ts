export type PowerAction = 'start' | 'stop' | 'reboot'

export type OperationStatus =
  | 'queued'
  | 'running'
  | 'verifying'
  | 'succeeded'
  | 'failed'
  | 'timed_out'
  | 'cancelled'

export type Operation = {
  id: string
  server_id: string
  connection_id: string
  action: PowerAction
  status: OperationStatus
  provider_request_id?: string
  error_code?: string
  error_message?: string
  queued_at: string
  started_at: string | null
  finished_at: string | null
  updated_at: string
}
