export type BackupKind = 'manual' | 'safety'
export type BackupMetadata = {
  id: string
  filename: string
  size_bytes: number
  sha256: string
  format_version: number
  kind: BackupKind
  status: 'ready' | 'failed'
  manifest: Record<string, number>
  created_at: string
}

export type BackupValidation = {
  format_version: number
  application_version: string
  created_at: string
  manifest: Record<string, number>
}
