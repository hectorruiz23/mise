import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import { api, ApiError } from '../api'
import { formatDateTime, formatRelative, isoDate, leaseMinutesLeft } from '../dates'
import { destinationsFor, requiresReason } from '../permissions'
import type { State, Task, TaskDetail as TaskDetailData } from '../types'
import { actorLabel, PRIORITY_LABELS, SIZE_LABELS, STATE_LABELS } from '../types'
import { Comments } from './Comments'
import { Deliveries } from './Deliveries'
import { Movements } from './Movements'

const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

export type ReviewQueue = {
  position: number
  total: number
  canPrev: boolean
  canNext: boolean
  onPrev: () => void
  onNext: () => void
  onExit: () => void
  onApproved: () => void
}

export function TaskDetail({
  taskKey,
  now,
  queue,
  onClose,
  onMove,
  onReloadBoard,
  onSessionExpired,
}: {
  taskKey: string
  now: number
  queue?: ReviewQueue
  onClose: () => void
  onMove: (task: Task, destination: State, reason: string) => Promise<void>
  onReloadBoard: () => Promise<void>
  onSessionExpired: () => void
}) {
  const [detail, setDetail] = useState<TaskDetailData | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [notice, setNotice] = useState('')
  const [menuOpen, setMenuOpen] = useState(false)
  const [destination, setDestination] = useState<State | ''>('')
  const [reason, setReason] = useState('')
  const [moveError, setMoveError] = useState('')
  const [moving, setMoving] = useState(false)
  const dialog = useRef<HTMLElement>(null)
  const closeButton = useRef<HTMLButtonElement>(null)
  const reasonInput = useRef<HTMLTextAreaElement>(null)
  const firstMenuItem = useRef<HTMLButtonElement>(null)
  const lastTrigger = useRef<HTMLButtonElement | null>(null)
  const menuOpenRef = useRef(false)
  menuOpenRef.current = menuOpen
  const queueRef = useRef(queue)
  queueRef.current = queue
  const taskRef = useRef<Task | undefined>(undefined)
  const runMoveRef = useRef<(to: State, why: string) => Promise<void>>(async () => {})
  const generation = useRef(0)

  const load = useCallback(
    async (quiet = false) => {
      const current = ++generation.current
      if (!quiet) setLoading(true)
      try {
        const next = await api.detail(taskKey)
        if (current !== generation.current) return
        setDetail(next)
        setLoadError('')
      } catch (caught) {
        if (current !== generation.current) return
        if (caught instanceof ApiError && caught.status === 401) {
          onSessionExpired()
          return
        }
        setLoadError(caught instanceof ApiError ? caught.message : 'The card could not be loaded.')
      } finally {
        if (current === generation.current) setLoading(false)
      }
    },
    [taskKey, onSessionExpired],
  )

  useEffect(() => {
    setDetail(null)
    setLoading(true)
    setNotice('')
    setMenuOpen(false)
    setDestination('')
    setReason('')
    setMoveError('')
    void load()
  }, [load])

  useEffect(() => {
    const before = document.activeElement instanceof HTMLElement ? document.activeElement : null
    closeButton.current?.focus()
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const root = document.getElementById('root')
    const hadInert = root?.hasAttribute('inert') ?? false
    const hadHidden = root?.getAttribute('aria-hidden') ?? null
    root?.setAttribute('inert', '')
    root?.setAttribute('aria-hidden', 'true')
    return () => {
      document.body.style.overflow = overflow
      if (!hadInert) root?.removeAttribute('inert')
      if (hadHidden === null) root?.removeAttribute('aria-hidden')
      else if (root) root.setAttribute('aria-hidden', hadHidden)
      before?.focus()
    }
  }, [])

  useEffect(() => {
    const onFocus = () => void load(true)
    const onKey = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.stopPropagation()
        if (menuOpenRef.current) {
          setMenuOpen(false)
          lastTrigger.current?.focus()
          return
        }
        onClose()
        return
      }
      const currentQueue = queueRef.current
      if (!currentQueue || menuOpenRef.current) return
      const target = event.target
      if (target instanceof HTMLElement && (target.isContentEditable || target.closest('input, textarea, select'))) {
        return
      }
      if (event.key === 'j') {
        if (currentQueue.canNext) currentQueue.onNext()
      } else if (event.key === 'k') {
        if (currentQueue.canPrev) currentQueue.onPrev()
      } else if ((event.key === 'd' || event.key === 'D') && taskRef.current?.state === 'review') {
        void runMoveRef.current('done', '')
      }
    }
    window.addEventListener('focus', onFocus)
    document.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('focus', onFocus)
      document.removeEventListener('keydown', onKey)
    }
  }, [load, onClose])

  useEffect(() => {
    if (menuOpen) firstMenuItem.current?.focus()
  }, [menuOpen])

  useEffect(() => {
    if (destination) reasonInput.current?.focus()
  }, [destination])

  function trapFocus(event: KeyboardEvent<HTMLElement>) {
    if (event.key !== 'Tab') return
    const nodes = Array.from(dialog.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? []).filter(
      node => node.getClientRects().length > 0,
    )
    if (nodes.length === 0) return
    const first = nodes[0]
    const last = nodes[nodes.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first.focus()
    }
  }

  const task = detail?.task
  const destinations = task ? destinationsFor(task, detail ?? undefined) : []
  const reasonRequired = task !== undefined && destination !== '' && requiresReason(task.state, destination)

  const liveLease = detail?.leases.find(lease => lease.released_at === null && lease.expires_at > now)
  const latestOpenLease = detail?.leases
    .filter(lease => lease.released_at === null)
    .sort((a, b) => b.id - a.id)[0]
  const expiredLease = !liveLease && latestOpenLease && latestOpenLease.expires_at <= now
  const leaseLine = liveLease
    ? `Leased by ${actorLabel(liveLease.agent, liveLease.agent_id)}, expires in ${leaseMinutesLeft(liveLease.expires_at, now)} min`
    : expiredLease
      ? 'Lease expired'
      : ''

  function openMenu(trigger: HTMLButtonElement) {
    lastTrigger.current = trigger
    setMoveError('')
    setDestination('')
    setReason('')
    setMenuOpen(true)
  }

  function closeMenu(returnFocus = true) {
    setMenuOpen(false)
    setDestination('')
    setReason('')
    setMoveError('')
    if (returnFocus) lastTrigger.current?.focus()
  }

  async function runMove(to: State, why: string) {
    if (!task) return
    if (requiresReason(task.state, to) && !why.trim()) {
      setMoveError(
        task.state === 'blocked' ? 'Write how the block was resolved.' : 'Write the reason for blocking the card.',
      )
      reasonInput.current?.focus()
      return
    }

    setMoving(true)
    setMoveError('')
    setNotice('')
    try {
      await onMove(task, to, why.trim())
      if (to === 'done') {
        if (queue) queue.onApproved()
        else onClose()
        return
      }
      closeMenu(false)
      await load(true)
      setNotice(`Moved to ${STATE_LABELS[to]}. The movement is recorded.`)
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) {
        onSessionExpired()
        return
      }
      if (caught instanceof ApiError && (caught.status === 412 || caught.status === 428)) {
        setDestination('')
        setReason('')
        await Promise.all([load(true), onReloadBoard()])
        closeMenu(false)
        setNotice('This card changed while the sheet was open. Reloading the latest version.')
        return
      }
      setMoveError(caught instanceof ApiError ? caught.message : 'The move failed. Try again.')
    } finally {
      setMoving(false)
    }
  }

  taskRef.current = task
  runMoveRef.current = runMove

  function choose(state: State) {
    if (!task) return
    if (requiresReason(task.state, state)) {
      setMoveError('')
      setDestination(state)
      return
    }
    void runMove(state, '')
  }

  function submitReason(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (destination) void runMove(destination, reason)
  }

  async function postComment(body: string) {
    try {
      await api.postComment(taskKey, body)
      await load(true)
      setNotice('Comment posted.')
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) {
        onSessionExpired()
      }
      throw caught
    }
  }

  const stateLabel = task ? STATE_LABELS[task.state] : 'Card detail'

  const stateButton = (where: 'block' | 'foot') => (
    <button
      type="button"
      className={`ficha-estado-btn${where === 'foot' ? ' ficha-estado-btn-pie' : ''}`}
      aria-haspopup="menu"
      aria-expanded={menuOpen}
      onClick={event => (menuOpen ? closeMenu() : openMenu(event.currentTarget))}
    >
      <span>{stateLabel}</span>
      <span className="ficha-caret" aria-hidden="true" />
    </button>
  )

  const menu = task && (
    <div
      className="ficha-menu"
      role="menu"
      aria-label="Move this card"
      onKeyDown={event => {
        if (event.key === 'Escape') {
          event.stopPropagation()
          closeMenu()
        }
      }}
    >
      <p className="ficha-menu-titulo">{destination ? 'Reason' : 'Move to'}</p>

      {destination ? (
        <form className="ficha-menu-form" onSubmit={submitReason} noValidate>
          <label className="opcional" htmlFor="ficha-reason">
            {task.state === 'blocked' ? 'How it was resolved' : 'Required'}
          </label>
          <textarea
            ref={reasonInput}
            id="ficha-reason"
            rows={3}
            maxLength={10000}
            value={reason}
            aria-invalid={Boolean(moveError)}
            onChange={event => {
              setReason(event.target.value)
              setMoveError('')
            }}
          />
          <div className="ficha-menu-acciones">
            <button type="submit" className="primario" disabled={moving}>
              {moving ? 'Moving…' : `Move to ${STATE_LABELS[destination]}`}
            </button>
            <button type="button" className="secundario" onClick={() => closeMenu()}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        destinations.map((state, index) => (
          <button
            key={state}
            ref={index === 0 ? firstMenuItem : undefined}
            type="button"
            role="menuitem"
            className={state === 'done' ? 'ficha-menu-primario' : undefined}
            disabled={moving}
            onClick={() => choose(state)}
          >
            {STATE_LABELS[state]}
          </button>
        ))
      )}

      {moveError && (
        <p className="ficha-menu-error" role="alert">
          {moveError}
        </p>
      )}

      {!destination && (
        <p className="ficha-menu-ayuda">
          {destinations.includes('done')
            ? 'Done closes the card in your name; the delivery below is what you are approving.'
            : 'The move is recorded in your name.'}
        </p>
      )}
    </div>
  )

  return createPortal(
    <div
      className="ficha-capa"
      onClick={event => {
        if (event.target !== event.currentTarget) return
        if (menuOpen) {
          closeMenu()
          return
        }
        onClose()
      }}
    >
      <article
        ref={dialog}
        className="ficha-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={detail ? 'ficha-title' : undefined}
        aria-label={detail ? undefined : 'Card detail'}
        onKeyDown={trapFocus}
      >
        {queue && (
          <div className="ficha-cola" role="group" aria-label="Review queue">
            <p className="ficha-cola-cuenta">
              Review <span className="dato">{queue.position}</span> of <span className="dato">{queue.total}</span>
            </p>
            <span className="ficha-key">{taskKey}</span>
            <div className="ficha-cola-flechas">
              <button type="button" onClick={queue.onPrev} disabled={!queue.canPrev} aria-label="Previous card in the queue">
                ‹
              </button>
              <button type="button" onClick={queue.onNext} disabled={!queue.canNext} aria-label="Next card in the queue">
                ›
              </button>
            </div>
            {task?.state === 'review' ? (
              <button type="button" className="primario" disabled={moving} onClick={() => void runMove('done', '')}>
                Done
              </button>
            ) : (
              <span className="ficha-cola-fuera">No longer in review</span>
            )}
            <button type="button" className="sheet-close" onClick={queue.onExit}>
              Leave the queue
            </button>
          </div>
        )}
        <div className="ficha-top">
          <div className="ficha-top-ref">
            <p className="sheet-kicker">Card detail</p>
            <span className="ficha-sep" aria-hidden="true" />
            <span className="ficha-key">{taskKey}</span>
          </div>
          <button
            ref={closeButton}
            type="button"
            className="sheet-close"
            aria-label="Close card detail"
            onClick={onClose}
          >
            Close
          </button>
        </div>

        {loading && !detail ? (
          <div className="ficha-cargando">
            <p role="status">Loading card…</p>
          </div>
        ) : loadError && !detail ? (
          <div className="ficha-cargando">
            <div className="aviso error" role="alert">
              <span>{loadError}</span>
              <button type="button" className="secundario" onClick={() => void load()}>
                Retry
              </button>
            </div>
          </div>
        ) : detail && task ? (
          <div className="ficha-columnas">
            <div className="ficha-columna ficha-texto" role="region" aria-label="Card" tabIndex={0}>
              <header className="sheet-header">
                <div className="sheet-reference">
                  <span className="proyecto">{task.project}</span>
                  <span className="dato">{task.key}</span>
                </div>
                <h2 id="ficha-title">{task.title}</h2>
                <div className="sheet-badges" aria-label="Card priority and size">
                  <span className="state-pill">{PRIORITY_LABELS[task.priority]} priority</span>
                  <span className="state-pill">{task.size ? SIZE_LABELS[task.size] : 'No size'}</span>
                </div>
                {leaseLine && (
                  <p className={`lease sheet-lease${leaseLine === 'Lease expired' ? ' lease-expired' : ''}`}>
                    {leaseLine}
                  </p>
                )}

                <dl className="sheet-meta">
                  <div>
                    <dt>Project</dt>
                    <dd>{task.project}</dd>
                  </div>
                  <div>
                    <dt>Version</dt>
                    <dd className="dato">v{task.version}</dd>
                  </div>
                  <div>
                    <dt>Created</dt>
                    <dd className="dato">
                      <time dateTime={isoDate(task.created_at)}>{formatDateTime(task.created_at)}</time>{' '}
                      <span className="relative">({formatRelative(task.created_at, now)})</span>
                    </dd>
                  </div>
                  <div>
                    <dt>Updated</dt>
                    <dd className="dato">
                      <time dateTime={isoDate(task.updated_at)}>{formatDateTime(task.updated_at)}</time>{' '}
                      <span className="relative">({formatRelative(task.updated_at, now)})</span>
                    </dd>
                  </div>
                </dl>

                {task.notes && (
                  <div className="sheet-text">
                    <h3>Notes</h3>
                    <p>{task.notes}</p>
                  </div>
                )}
                {task.acceptance && (
                  <div className="sheet-text">
                    <h3>Acceptance</h3>
                    <p>{task.acceptance}</p>
                  </div>
                )}
              </header>

              {notice && (
                <p className="sheet-notice" role="status" aria-live="polite">
                  {notice}
                </p>
              )}

              <Comments comments={detail.comments} now={now} onPost={postComment} />
            </div>

            <div className="ficha-columna ficha-actividad" role="region" aria-label="Activity" tabIndex={0}>
              <div className="ficha-estado">
                <div className="ficha-estado-fila">
                  <span className="ficha-estado-label">State</span>
                  {stateButton('block')}
                </div>
                {menuOpen && menu}
              </div>

              <Deliveries deliveries={detail.deliveries} task={task} now={now} />
              <Movements movements={detail.movements} now={now} />
            </div>
          </div>
        ) : null}

        {detail && task && <div className="ficha-pie">{stateButton('foot')}</div>}
      </article>
    </div>,
    document.body,
  )
}
