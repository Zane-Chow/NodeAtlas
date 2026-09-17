export type Capability = { available?: boolean; reason?: string }
export type ServerCapabilities = {
  can_start?: Capability; can_stop?: Capability; can_reboot?: Capability
  can_embed_console?: Capability; can_open_console_window?: Capability; has_provider_portal?: Capability
}
export type Server = {
  id: string; connection_id: string; external_id: string; scope: string; name: string
  state: string; remote_state: string; spec: { cpu?: number; memory_mb?: number }
  addresses: Array<{ type?: string; address: string }>; capabilities: ServerCapabilities
  portal_url?: string; last_seen_at: string; last_state_checked_at: string; created_at: string; updated_at: string
}
