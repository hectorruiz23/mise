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

  const environment = plan.target_environment ?? ''

  return (
    <main className="contenedor principal plan-page">
      <header className="plan-heading">
        <a href="#plans">← Plans</a>
        <div className="plan-heading-line">
          <h1>Plan #{plan.id}</h1>
          <span className="plan-project">{plan.project}</span>
        </div>
        <p className="plan-metadata">
          <strong>{PLAN_STATE_LABELS[plan.state]}</strong>
          {Number.isFinite(plan.approved_at) && (
            <span>Approved <time dateTime={isoDate(plan.approved_at)}>{formatDateTime(plan.approved_at)}</time></span>
          )}
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

      <div className="plan-sheet">
        <PlanTaskGraph nodes={plan.nodes} edges={plan.edges} />
        <section className="plan-acceptance">
          <h2>Acceptance</h2>
          <div>
            <code>{plan.scenario}</code>
            <p>
              {environment
                ? `The plan closes once the merged revision is serving in ${environment}.`
                : 'No environment was agreed. The plan closes once the merged revision is serving everywhere the project is deployed.'}
            </p>
          </div>
        </section>
      </div>
      <footer className="plan-footer"><a href="#board">Back to the board</a></footer>
    </main>
  )
}
