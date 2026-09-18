import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { BackupsPage } from './BackupsPage'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('lists backups and creates one without retaining the passphrase', async () => {
  let created = false
  const requests: Array<{ url: string; init?: RequestInit }> = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = String(input)
    requests.push({ url, init })
    if (init?.method === 'POST') {
      created = true
      return new Response(JSON.stringify({ backup: backup('backup-b', 'safety') }), { status: 201 })
    }
    return new Response(JSON.stringify({ backups: created ? [backup('backup-b', 'safety'), backup('backup-a', 'manual')] : [backup('backup-a', 'manual')], total: created ? 2 : 1 }), { status: 200 })
  })
  render(<BackupsPage />)
  expect(await screen.findByText('controlpanel-backup-a.scpb')).toBeInTheDocument()
  await userEvent.type(screen.getByLabelText('备份口令'), 'correct horse backup passphrase')
  await userEvent.type(screen.getByLabelText('确认备份口令'), 'correct horse backup passphrase')
  await userEvent.click(screen.getByRole('button', { name: '创建加密备份' }))
  expect(await screen.findByText('备份创建完成')).toBeInTheDocument()
  expect(screen.getByLabelText('备份口令')).toHaveValue('')
  expect(screen.getByLabelText('确认备份口令')).toHaveValue('')
  expect(requests.some(({ url, init }) => url.endsWith('/backups') && init?.method === 'POST' && String(init.body).includes('correct horse backup passphrase'))).toBe(true)
})

it('requires destructive confirmation and clears restore passphrase', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    if (String(input).endsWith('/restore') && init?.method === 'POST') return new Response(JSON.stringify({ status: 'restored' }), { status: 200 })
    return new Response(JSON.stringify({ backups: [backup('backup-a', 'manual')], total: 1 }), { status: 200 })
  })
  render(<BackupsPage />)
  await screen.findByText('controlpanel-backup-a.scpb')
  await userEvent.click(screen.getByRole('button', { name: '恢复 backup-a' }))
  const dialog = screen.getByRole('dialog', { name: '恢复备份' })
  await userEvent.type(within(dialog).getByLabelText('恢复口令'), 'correct horse backup passphrase')
  await userEvent.click(within(dialog).getByRole('button', { name: '确认恢复并覆盖当前数据' }))
  expect(await screen.findByText('恢复完成，请重新加载页面并登录')).toBeInTheDocument()
  await waitFor(() => expect(within(dialog).queryByDisplayValue('correct horse backup passphrase')).not.toBeInTheDocument())
})

function backup(id: string, kind: string) {
  return { id, filename: `controlpanel-${id}.scpb`, size_bytes: 1024, sha256: 'abc', format_version: 1, kind, status: 'ready', manifest: { users: 1 }, created_at: '2026-09-18T12:00:00Z' }
}
