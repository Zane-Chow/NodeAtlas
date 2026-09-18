import { apiRequest } from '../api/client'
import type { BackupMetadata, BackupValidation } from './types'

export async function listBackups(): Promise<BackupMetadata[]> {
  return (await apiRequest<{ backups: BackupMetadata[]; total: number }>('/backups')).backups
}

export async function createBackup(passphrase: string): Promise<BackupMetadata> {
  return (await apiRequest<{ backup: BackupMetadata }>('/backups', { method: 'POST', body: JSON.stringify({ passphrase }) })).backup
}

export async function validateBackup(backupID: string, passphrase: string): Promise<BackupValidation> {
  return (await apiRequest<{ validation: BackupValidation }>('/backups/validate', { method: 'POST', body: JSON.stringify({ backup_id: backupID, passphrase }) })).validation
}

export async function restoreBackup(backupID: string, passphrase: string): Promise<void> {
  await apiRequest(`/backups/${encodeURIComponent(backupID)}/restore`, { method: 'POST', body: JSON.stringify({ passphrase }) })
}

export async function downloadBackup(backup: BackupMetadata): Promise<void> {
  const response = await fetch(`/api/v1/backups/${encodeURIComponent(backup.id)}/download`, { credentials: 'include' })
  if (!response.ok) throw new Error('Unable to download backup')
  const objectURL = URL.createObjectURL(await response.blob())
  const link = document.createElement('a')
  link.href = objectURL
  link.download = backup.filename
  link.click()
  URL.revokeObjectURL(objectURL)
}
