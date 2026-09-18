import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { createBackup, downloadBackup, listBackups, restoreBackup, validateBackup } from './api'
import type { BackupMetadata } from './types'

type DialogState = { mode: 'validate' | 'restore'; backup: BackupMetadata }

export function BackupsPage() {
  const [backups, setBackups] = useState<BackupMetadata[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [passphrase, setPassphrase] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [dialogPassphrase, setDialogPassphrase] = useState('')

  const refresh = useCallback(async () => {
    try {
      setBackups(await listBackups())
      setError('')
    } catch {
      setError('无法加载备份列表')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void Promise.resolve().then(refresh) }, [refresh])

  async function create(event: FormEvent) {
    event.preventDefault()
    if (passphrase.length < 12 || passphrase !== confirmation) {
      setError('备份口令至少 12 个字符，且两次输入必须一致')
      return
    }
    setSubmitting(true)
    setError('')
    setNotice('')
    try {
      await createBackup(passphrase)
      setNotice('备份创建完成')
      await refresh()
    } catch {
      setError('创建备份失败')
    } finally {
      setPassphrase('')
      setConfirmation('')
      setSubmitting(false)
    }
  }

  function openDialog(mode: DialogState['mode'], backup: BackupMetadata) {
    setDialog({ mode, backup })
    setDialogPassphrase('')
    setError('')
  }

  async function submitDialog(event: FormEvent) {
    event.preventDefault()
    if (!dialog || dialogPassphrase.length < 12) {
      setError('请输入至少 12 个字符的备份口令')
      return
    }
    const current = dialog
    setSubmitting(true)
    setError('')
    setNotice('')
    try {
      if (current.mode === 'validate') {
        const result = await validateBackup(current.backup.id, dialogPassphrase)
        setNotice(`备份校验通过：格式 v${result.format_version}`)
      } else {
        await restoreBackup(current.backup.id, dialogPassphrase)
        setNotice('恢复完成，请重新加载页面并登录')
      }
      setDialog(null)
    } catch {
      setError(current.mode === 'validate' ? '备份校验失败' : '备份恢复失败；当前数据未被替换')
    } finally {
      setDialogPassphrase('')
      setSubmitting(false)
    }
  }

  async function download(backup: BackupMetadata) {
    setError('')
    try { await downloadBackup(backup) } catch { setError('下载备份失败') }
  }

  return <section className="workspace-page">
    <div className="page-heading"><div><p className="eyebrow">Encrypted recovery</p><h2>备份与设置</h2><p>创建可在 SQLite 与 MySQL 间恢复的应用级加密归档。</p></div><span className="count-badge">{backups.length} 份</span></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="inline-notice" role="status">{notice}</p>}
    <article className="backup-create-card">
      <div><h3>创建加密备份</h3><p>口令只用于本次加密，不会由面板保存。请在安全的密码管理器中保管。</p></div>
      <form onSubmit={create}><label>备份口令<input aria-label="备份口令" type="password" value={passphrase} onChange={(event) => setPassphrase(event.target.value)} autoComplete="new-password" /></label><label>确认备份口令<input aria-label="确认备份口令" type="password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} autoComplete="new-password" /></label><button className="primary-button compact" disabled={submitting}>创建加密备份</button></form>
    </article>
    {loading ? <p className="empty-state">正在加载备份…</p> : backups.length === 0 ? <p className="empty-state">还没有备份</p> : <div className="backup-list">
      {backups.map((backup) => <article className="backup-card" key={backup.id}>
        <div><strong>{backup.filename}</strong><small>{backup.kind === 'safety' ? '恢复前安全快照' : '手动备份'} · {formatBytes(backup.size_bytes)} · {new Date(backup.created_at).toLocaleString()}</small></div>
        <div className="card-actions"><button onClick={() => void download(backup)}>下载</button><button onClick={() => openDialog('validate', backup)}>校验</button><button className="danger-button" aria-label={`恢复 ${backup.id}`} onClick={() => openDialog('restore', backup)}>恢复</button></div>
      </article>)}
    </div>}
    {dialog && <div className="modal-backdrop" onClick={() => setDialog(null)}><form className="modal-card" role="dialog" aria-modal="true" aria-label={dialog.mode === 'restore' ? '恢复备份' : '校验备份'} onSubmit={submitDialog} onClick={(event) => event.stopPropagation()}>
      <p className="eyebrow">{dialog.backup.filename}</p><h3>{dialog.mode === 'restore' ? '恢复备份' : '校验备份'}</h3>
      <p>{dialog.mode === 'restore' ? '当前数据库数据将被该归档替换。系统会先创建一份安全快照，并在恢复后撤销现有登录会话。' : '输入创建该归档时使用的口令，校验不会修改当前数据。'}</p>
      <label>{dialog.mode === 'restore' ? '恢复口令' : '校验口令'}<input aria-label={dialog.mode === 'restore' ? '恢复口令' : '校验口令'} type="password" value={dialogPassphrase} onChange={(event) => setDialogPassphrase(event.target.value)} autoComplete="current-password" /></label>
      <div className="modal-actions"><button type="button" disabled={submitting} onClick={() => setDialog(null)}>取消</button><button className={dialog.mode === 'restore' ? 'danger-button' : 'primary-button compact'} disabled={submitting}>{dialog.mode === 'restore' ? '确认恢复并覆盖当前数据' : '开始校验'}</button></div>
    </form></div>}
  </section>
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}
