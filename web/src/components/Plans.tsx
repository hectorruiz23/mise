import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import { capabilitiesFor } from '../permissions'
import { isViewer, type Me, type PlanDraftSummary, type PlanGraph } from '../types'
import { ViewTabs } from './Views'

type Props = {
  me: Me
  onSignIn: () => void
  onSessionExpired: () => void
}

function PlansHeader({ me, detail }: { me: Me; detail?: string }) {
  return (
    <header className="propuesta-cabecera">
      <h1>Plans</h1>
      {detail && <p className="dato">{detail}</p>}
      <ViewTabs me={me} caps={capabilitiesFor(me)} activeView="plans" />
    </header>
  )
}

export function PlansPage({ me, onSignIn, onSessionExpired }: Props) {
  const [drafts, setDrafts] = useState<PlanDraftSummary[]>([])
  const [plans, setPlans] = useState<PlanGraph[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setError('')
    try {
      const [nextDrafts, nextPlans] = await Promise.all([api.planDrafts(), api.plans()])
      setDrafts(nextDrafts)
      setPlans(nextPlans)
      setLoaded(true)
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) {
        onSessionExpired()
        return
      }
      setError(caught instanceof ApiError ? caught.message : 'Cannot reach the server.')
    }
  }, [onSessionExpired])

  useEffect(() => {
    if (isViewer(me)) return
    void load()
  }, [load, me])

  if (isViewer(me)) {
    return (
      <main className="contenedor principal">
        <PlansHeader me={me} detail="Proposals and plans need a session." />
        <section className="abertura">
          <div className="propuesta-cuerpo">
            <div>
              <p className="propuesta-resultado">Sign in to read the proposals waiting for you and the plans in flight.</p>
              <p className="propuesta-criterios">Anyone can read the board; proposals and plans stay behind a credential.</p>
            </div>
            <div className="propuesta-pie">
              <button type="button" className="primario" onClick={onSignIn}>
                Sign in
              </button>
              <a href="#board">Back to the board</a>
            </div>
          </div>
        </section>
      </main>
    )
  }

  if (error) {
    return (
      <main className="contenedor principal">
        <PlansHeader me={me} />
        <p className="error" role="alert">
          {error}
        </p>
        <button type="button" className="secundario" onClick={() => void load()}>
          Retry
        </button>
      </main>
    )
  }

  if (!loaded) {
    return (
      <main className="contenedor principal">
        <PlansHeader me={me} />
        <p role="status">Loading plans…</p>
      </main>
    )
  }

  const pending = drafts.filter(draft => draft.state === 'draft')
  const running = plans.filter(plan => plan.state === 'active')
  const pendingLabel = `${pending.length} pending ${pending.length === 1 ? 'proposal' : 'proposals'}`
  const runningLabel = `${running.length} running ${running.length === 1 ? 'plan' : 'plans'}`
  const summary = `${pendingLabel} · ${runningLabel}`

  return (
    <main className="contenedor principal">
      <PlansHeader me={me} detail={summary} />

      <section className="abertura">
        <div className="propuesta-cuerpo">
          <div>
            <h2>Pending proposals</h2>
            {pending.length === 0 ? (
              <p className="dato">No proposal is waiting for approval.</p>
            ) : (
              <ul className="lista-planes">
                {pending.map(draft => (
                  <li key={draft.id}>
                    <a className="fila-plan" href={`#draft/${draft.id}`}>
                      <span className="fila-plan-titulo">Proposal #{draft.id}</span>
                      <span className="fila-plan-estado">Awaiting approval</span>
                      <span className="dato">
                        {draft.project} · version {draft.version}
                      </span>
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div>
            <h2>Running plans</h2>
            {running.length === 0 ? (
              <p className="dato">No plan is running.</p>
            ) : (
              <ul className="lista-planes">
                {running.map(plan => {
                  const done = plan.nodes.filter(node => node.state === 'done').length
                  return (
                    <li key={plan.id}>
                      <a className="fila-plan" href={`#plan/${plan.id}`}>
                        <span className="fila-plan-titulo">Plan #{plan.id}</span>
                        <span className="fila-plan-estado">Running</span>
                        <span className="dato">
                          {plan.project} · {done} of {plan.nodes.length} tasks done
                        </span>
                      </a>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>

          <div className="propuesta-pie">
            <a href="#board">Back to the board</a>
          </div>
        </div>
      </section>
    </main>
  )
}
