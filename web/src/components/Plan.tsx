import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import { formatDateTime, isoDate } from '../dates'
import { STATE_LABELS, type PlanGraph } from '../types'

const PLAN_STATE_LABELS: Record<PlanGraph['state'], string> = {
  active: 'Running',
  completed: 'Completed',
  cancelled: 'Cancelled',
}

type Props = {
  planID: number
  onSessionExpired: () => void
}

export function PlanPage({ planID, onSessionExpired }: Props) {
  const [plan, setPlan] = useState<PlanGraph | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setError('')
    try {
      setPlan(await api.plan(planID))
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) {
        onSessionExpired()
        return
      }
      setError(caught instanceof ApiError ? caught.message : 'Cannot reach the server.')
    }
  }, [onSessionExpired, planID])

  useEffect(() => {
    void load()
  }, [load])

  if (error) {
    return (
      <main className="contenedor principal">
        <p className="error" role="alert">
          {error}
        </p>
        <p>
          <a href="#plans">Back to plans</a>
        </p>
      </main>
    )
  }

  if (!plan) {
    return (
      <main className="contenedor principal">
        <p role="status">Loading plan…</p>
      </main>
    )
  }

  const done = plan.nodes.filter(node => node.state === 'done').length
  const environment = plan.target_environment ?? ''

  return (
    <main className="contenedor principal">
      <header className="propuesta-cabecera">
        <h1>Plan #{plan.id}</h1>
        <p className="dato">
          {plan.project} · {PLAN_STATE_LABELS[plan.state]} · approved{' '}
          <time dateTime={isoDate(plan.approved_at)}>{formatDateTime(plan.approved_at)}</time>
        </p>
      </header>

      {plan.state === 'completed' && (
        <p className="propuesta-aviso" role="status">
          This plan is complete: the approved revision is serving in{' '}
          {environment || 'every environment where the project is deployed'}.
        </p>
      )}
      {plan.state === 'cancelled' && (
        <p className="propuesta-aviso" role="status">
          This plan was cancelled. Its remaining tasks will not run as part of it.
        </p>
      )}

      <section className="abertura">
        <div className="propuesta-cuerpo">
          <div>
            <h2>Acceptance</h2>
            <p className="propuesta-comando">
              <code>{plan.scenario}</code>
            </p>
            <p className="propuesta-criterios">
              {environment
                ? `The plan closes once the merged revision is serving in ${environment}.`
                : 'No environment was agreed: the plan closes once the merged revision is serving everywhere the project is deployed.'}
            </p>
          </div>

          <div>
            <h2>Tasks</h2>
            <p className="propuesta-criterios">
              {done} of {plan.nodes.length} tasks done.
            </p>
            <ol className="propuesta-tareas">
              {plan.nodes.map(node => (
                <li className="propuesta-tarea" key={node.task_key}>
                  <p className="propuesta-titulo">
                    <code className="propuesta-clave">{node.task_key}</code>{' '}
                    <strong>{node.title}</strong>
                    {node.reveals && <span className="propuesta-marca">reveals the change</span>}
                  </p>
                  <p className="plan-tarea-estado">
                    <span className={`estado-tarea estado-${node.state}`}>
                      {STATE_LABELS[node.state]}
                    </span>
                    {node.state === 'backlog' && node.waiting_on.length > 0 && (
                      <span className="dato">
                        Waiting for {joinKeys(node.waiting_on)} before it can start
                      </span>
                    )}
                    {node.proof && (
                      <span className="dato">
                        <a href={node.proof.pr_url} target="_blank" rel="noreferrer">
                          Pull request
                        </a>
                      </span>
                    )}
                  </p>
                </li>
              ))}
            </ol>
          </div>

          <div className="propuesta-pie">
            <a href="#plans">Back to plans</a>
            <a href="#board">Back to the board</a>
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
