import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import { STATE_LABELS } from '../types'
import type { Me, PlanDraft } from '../types'

type Props = {
  me: Me
  draftID: number
  onBrowseBoard: () => void
}

export function PlanDraftPage({ me, draftID, onBrowseBoard }: Props) {
  const [draft, setDraft] = useState<PlanDraft | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [approving, setApproving] = useState(false)

  const load = useCallback(async () => {
    setError('')
    try {
      setDraft(await api.planDraft(draftID))
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : 'Cannot reach the server.')
    }
  }, [draftID])

  useEffect(() => {
    void load()
  }, [load])

  async function approve(current: PlanDraft) {
    setApproving(true)
    setNotice('')
    try {
      const approved = await api.approvePlanDraft(current.id, current.version)
      setDraft(approved)
      setNotice(`Plan #${approved.plan_id} is running. The tasks can be taken from now on.`)
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'stale_version') {
        setNotice('This proposal changed after you opened it. Review the newer version and approve again.')
        await load()
      } else {
        setNotice(caught instanceof ApiError ? caught.message : 'Cannot reach the server.')
      }
    } finally {
      setApproving(false)
    }
  }

  if (error) {
    return (
      <main className="contenedor principal">
        <p className="error" role="alert">
          {error}
        </p>
      </main>
    )
  }

  if (!draft) {
    return (
      <main className="contenedor principal">
        <p role="status">Loading proposal…</p>
      </main>
    )
  }

  const byKey = new Map(draft.tasks.map(task => [task.key, task]))
  const waiting = new Map<string, string[]>()
  for (const edge of draft.edges) {
    waiting.set(edge.to, [...(waiting.get(edge.to) ?? []), edge.from])
  }
  const approved = draft.state === 'approved'
  const discarded = draft.state === 'discarded'

  return (
    <main className="contenedor principal">
      <section className="abertura">
        <h1>Proposal #{draft.id}</h1>
        <p className="dato">
          {draft.project} · version {draft.version} · {approved ? 'approved' : discarded ? 'discarded' : 'draft'}
        </p>

        <h2>What ships</h2>
        <p>{draft.outcome}</p>

        {draft.visual_review && (
          <div className="atencion">
            <h2>This changes what you see</h2>
            <p>{draft.visible_change}</p>
            <p>
              The work becomes visible only through <strong>{describeReveal(draft, byKey)}</strong>. Its pull
              request is never merged automatically, whatever its checks say.
            </p>
          </div>
        )}

        <h2>Tasks</h2>
        <ul>
          {draft.tasks.map(task => {
            const waits = waiting.get(task.key) ?? []
            return (
              <li key={task.key}>
                <strong>{task.title}</strong>{' '}
                <span className="dato">
                  {waits.length === 0 ? 'can start now' : `waits for ${waits.join(', ')}`}
                  {task.key === draft.reveal_key ? ' · reveals the change' : ''}
                </span>
                {task.acceptance && <p>{task.acceptance}</p>}
              </li>
            )
          })}
        </ul>

        <h2>How completion is established</h2>
        <p>
          <code>{draft.scenario}</code> over the integrated application, the merged revision serving in every
          environment, and this approval.
        </p>

        {notice && (
          <p className="aviso" role="status">
            {notice}
          </p>
        )}

        {me.kind === 'human' && !approved && !discarded && (
          <div className="acciones-mover">
            <button
              type="button"
              className="primario"
              disabled={approving}
              onClick={() => void approve(draft)}
            >
              {approving ? 'Approving…' : `Approve version ${draft.version}`}
            </button>
            <p className="dato">No task exists until you approve: nothing can be taken before this.</p>
          </div>
        )}

        {approved && (
          <p className="aviso" role="status">
            Approved. The cards exist and the plan is running.{' '}
            <button type="button" className="secundario" onClick={onBrowseBoard}>
              Back to the board
            </button>
          </p>
        )}
      </section>
    </main>
  )
}

function describeReveal(draft: PlanDraft, byKey: Map<string, PlanDraft['tasks'][number]>): string {
  if (!draft.reveal_key) return 'the revealing task'
  const task = byKey.get(draft.reveal_key)
  return task ? task.title : draft.reveal_key
}

export function stateLabel(state: string): string {
  return (STATE_LABELS as Record<string, string>)[state] ?? state
}
