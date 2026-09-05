import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { JobsPage } from '@/features/jobs/jobs-page'
import { adminRequest } from '@/lib/api'
import type { WorkflowJob } from '@/lib/types'

vi.mock('@/lib/api', () => ({ adminRequest: vi.fn(), formatTimestamp: () => '09:00' }))
const job: WorkflowJob = { id: 'fixture-job', protocol_id: 'fixture-pack', workflow_id: 'fixture-flow', state: 'running', attempts: 2, max_attempts: 2, created_at: '2026-09-05T00:00:00Z', updated_at: '2026-09-05T00:00:01Z', cancellable: true }
beforeEach(() => vi.mocked(adminRequest).mockReset())
describe('job management', () => {
  it('shows the result and error details', async () => {
    render(<JobsPage jobs={[{ ...job, state: 'failed', error: 'fixture upstream failure', result: { answer: 'fixture-result' } }]} events={[]} />)
    await userEvent.click(screen.getByRole('button', { name: '查看任务 fixture-job' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('fixture-result')
    expect(screen.getByRole('alert')).toHaveTextContent('fixture upstream failure')
  })
  it('confirms cancellation and calls it once even if refreshing the list fails', async () => {
    vi.mocked(adminRequest).mockResolvedValue({ ...job, state: 'cancelled', updated_at: '2026-09-05T00:00:02Z' })
    render(<JobsPage jobs={[job]} events={[]} adminToken='fixture-only' onChanged={vi.fn().mockRejectedValue(new Error('refresh failed'))} />)
    await userEvent.click(screen.getByRole('button', { name: '取消任务 fixture-job' }))
    expect(adminRequest).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: '确认取消' }))
    await waitFor(() => expect(screen.getByRole('dialog')).toHaveTextContent('cancelled'))
    expect(adminRequest).toHaveBeenCalledExactlyOnceWith('/local/api/workflows/fixture-pack/fixture-flow/fixture-job/cancel', 'fixture-only', { method: 'POST' })
  })
  it('does not offer cancellation for an unsupported or completed job', () => {
    render(<JobsPage jobs={[{ ...job, state: 'succeeded', cancellable: false }]} events={[]} />)
    expect(screen.queryByRole('button', { name: '取消任务 fixture-job' })).not.toBeInTheDocument()
  })
})
