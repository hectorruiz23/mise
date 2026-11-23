import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
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
      setDraft(await api.approvePlanDraft(current.id, current.version))
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'stale_version') {
        setNotice('This proposal changed after you opened it. Read the newer version and approve again.')
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

  const approved = draft.state === 'approved'
  const discarded = draft.state === 'discarded'
  const waiting = new Map<string, string[]>()
  for (const edge of draft.edges) {
    waiting.set(edge.to, [...(waiting.get(edge.to) ?? []), edge.from])
  }

  return (
    <main className="contenedor principal">
      <header className="propuesta-cabecera">
        <h1>Proposal #{draft.id}</h1>
        <p className="dato">
          {draft.project} · version {draft.version} ·{' '}
          {approved ? 'approved' : discarded ? 'discarded' : 'draft'}
        </p>
      </header>

      {notice && (
        <p className="propuesta-aviso" role="status">
          {notice}
        </p>
      )}

      <section className="abertura">
        <div className="propuesta-cuerpo">
          <div>
            <p className="propuesta-etiqueta">What ships</p>
            <p className="propuesta-resultado">{draft.outcome}</p>
          </div>

          <div>
            <h2>Tasks</h2>
            <ol className="propuesta-tareas">
              {draft.tasks.map(task => {
                const waits = waiting.get(task.key) ?? []
                const reveals = task.key === draft.reveal_key
                return (
                  <li className="propuesta-tarea" key={task.key}>
                    <p className="propuesta-titulo">
                      <code className="propuesta-clave">{task.key}</code>{' '}
                      <strong>{task.title}</strong>
                      {reveals && <span className="propuesta-marca">reveals the change</span>}
                    </p>
                    <p className="propuesta-dependencia">
                      {waits.length > 0
                        ? `Waits for ${joinKeys(waits)}`
                        : approved
                          ? 'No prerequisites'
                          : 'Can start together after approval'}
                    </p>
                    {task.acceptance && <p className="propuesta-criterios">{task.acceptance}</p>}
                    {reveals && draft.visual_review && (
                      <div className="propuesta-revision">
                        <p>{draft.visible_change}</p>
                        <p>You approve this task’s pull request on GitHub before it can be merged.</p>
                      </div>
                    )}
                  </li>
                )
              })}
            </ol>
          </div>

          <div>
            <h2>Completion check</h2>
            <p className="propuesta-comando">
              <code>{draft.scenario}</code>
            </p>
            <p className="propuesta-criterios">
              It runs over the integrated application, and the plan closes once the merged revision is
              serving in every environment.
            </p>
          </div>

          <div className="propuesta-pie">
            {approved ? (
              <>
                <p>Approved. The cards exist and the plan is running.</p>
                <button type="button" className="secundario" onClick={onBrowseBoard}>
                  Back to the board
                </button>
              </>
            ) : discarded ? (
              <p className="dato">This proposal was discarded. Nothing was created.</p>
            ) : me.kind === 'human' ? (
              <>
                <p className="dato">Tasks are created only when you approve this version.</p>
                <button
                  type="button"
                  className="primario"
                  disabled={approving}
                  onClick={() => void approve(draft)}
                >
                  {approving ? 'Approving…' : `Approve version ${draft.version}`}
                </button>
              </>
            ) : (
              <p className="dato">Only the owner can approve this proposal.</p>
            )}
          </div>
        </div>
      </section>
    </main>
  )
}

function joinKeys(keys: string[]): string {
  if (keys.length <= 1) return keys.join('')
  return `${keys.slice(0, -1).join(', ')} and ${keys[keys.length - 1]}`
}
