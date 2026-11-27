import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import { formatDateTime, isoDate } from '../dates'
import type { PlanGraph } from '../types'
import { PlanTaskGraph } from './PlanTaskGraph'

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
            <PlanTaskGraph nodes={plan.nodes} edges={plan.edges} />
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
