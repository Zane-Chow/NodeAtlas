export type ConsoleSession = {
  session_id: string
  ticket: string
  expires_at: string
  protocol?: 'terminal' | 'rfb'
  credentials?: { password?: string }
}

export type ExternalTarget = { url: string }
