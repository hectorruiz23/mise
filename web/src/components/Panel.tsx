import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { api, ApiError } from '../api'
import { normalize } from '../filters'
import type { CreateTaskInput, State, Task, View } from '../types'
import { STATE_LABELS } from '../types'
import { PAGE_SIZE, useBacklog } from '../useBacklog'
import { useBoard } from '../useBoard'
import { useProjects } from '../useProjects'
import { Backlog } from './Backlog'
import { Board } from './Board'
import { CreateTask } from './CreateTask'
import { TaskDetail, type ReviewQueue } from './TaskDetail'
import { ViewTabs } from './Views'

function parseHash(): { view: View; key: string | null; reviewing: boolean } {
  const [rawView, rawKey] = location.hash.replace(/^#/, '').split('/')
  const reviewing = rawView === 'review'
  const view: View = rawView === 'backlog' ? 'backlog' : 'board'
  const key = rawKey?.trim() ? decodeURIComponent(rawKey.trim()) : null
  return { view, key, reviewing }
}

export function Panel({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [view, setView] = useState<View>(() => parseHash().view)
  const viewRef = useRef(view)
  viewRef.current = view

  const [search, setSearch] = useState('')
  const [project, setProject] = useState('')
  const [notice, setNotice] = useState('')
  const [creating, setCreating] = useState(false)
  const [openKey, setOpenKey] = useState<string | null>(() => parseHash().key)
  const openedByUs = useRef(false)
  const [queue, setQueue] = useState<{ keys: string[]; index: number } | null>(null)
  const initialReview = useRef(parseHash().reviewing)
  const [createdTask, setCreatedTask] = useState<Task | null>(null)
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000))
  const [reservedHeight, setReservedHeight] = useState<number | null>(null)

  const contentRef = useRef<HTMLDivElement>(null)
  const scrollRef = useRef<Record<View, number>>({ board: 0, backlog: 0 })
  const lastOpenKey = useRef<string | null>(null)

  const board = useBoard(view === 'board')
  const backlog = useBacklog({ active: view === 'backlog', createdTask, onSessionExpired })

  const navigate = useCallback((next: View) => {
    if (viewRef.current === next) return
    const height = contentRef.current?.offsetHeight ?? null
    scrollRef.current[viewRef.current] = window.scrollY
    viewRef.current = next
    setSearch('')
    setProject('')
    setCreating(false)
    setOpenKey(null)
    setReservedHeight(height)
    setView(next)
  }, [])

  useLayoutEffect(() => {
    const top = scrollRef.current[view] ?? 0
    window.scrollTo(0, top)
    if (top === 0 || window.scrollY >= top) return
    const retry = window.setTimeout(() => window.scrollTo(0, top), 60)
    return () => window.clearTimeout(retry)
  }, [view])

  const openCard = useCallback((key: string) => {
    openedByUs.current = true
    location.hash = `#${viewRef.current}/${encodeURIComponent(key)}`
  }, [])

  const closeCard = useCallback(() => {
    if (openedByUs.current) {
      openedByUs.current = false
      history.back()
      return
    }
    history.replaceState(null, '', `#${viewRef.current}`)
    setOpenKey(null)
  }, [])

  const reviewSnapshot = useCallback(
    () =>
      board.tasks
        .filter(task => task.state === 'review')
        .sort((a, b) => a.created_at - b.created_at || a.key.localeCompare(b.key))
        .map(task => task.key),
    [board.tasks],
  )

  const startReview = useCallback(() => {
    const keys = reviewSnapshot()
    if (keys.length === 0) return
    setQueue({ keys, index: 0 })
    location.hash = '#review/' + encodeURIComponent(keys[0])
  }, [reviewSnapshot])

  const goTo = useCallback(
    (index: number) => {
      if (!queue) return
      const key = queue.keys[index]
      if (key === undefined) return
      setQueue({ keys: queue.keys, index })
      history.replaceState(null, '', '#review/' + encodeURIComponent(key))
    },
    [queue],
  )

  const advance = useCallback(async () => {
    await board.reload()
    if (!queue) return
    if (queue.index + 1 < queue.keys.length) {
      goTo(queue.index + 1)
      return
    }
    setQueue({ keys: queue.keys, index: queue.keys.length })
    history.replaceState(null, '', '#review')
  }, [board.reload, goTo, queue])

  const exitQueue = useCallback(() => {
    history.replaceState(null, '', '#board')
    setOpenKey(null)
    setQueue(null)
    window.setTimeout(() => document.getElementById('board-review')?.focus(), 0)
  }, [])

  useEffect(() => {
    const onHashChange = () => {
      const next = parseHash()
      navigate(next.view)
      setOpenKey(next.key)
      if (!next.reviewing) {
        setQueue(null)
        return
      }
      setQueue(current => {
        if (current) {
          const position = next.key ? current.keys.indexOf(next.key) : current.index
          if (position >= 0 && position !== current.index) return { keys: current.keys, index: position }
          return current
        }
        const keys = reviewSnapshot()
        if (keys.length === 0) return next.key ? { keys: [next.key], index: 0 } : null
        const position = next.key ? keys.indexOf(next.key) : 0
        if (position >= 0) return { keys, index: position }
        return next.key ? { keys: [next.key], index: 0 } : { keys, index: 0 }
      })
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [navigate, reviewSnapshot])

  useEffect(() => {
    if (!initialReview.current || board.board === null) return
    initialReview.current = false
    if (queue !== null) return
    const parsed = parseHash()
    if (!parsed.reviewing) return
    const keys = reviewSnapshot()
    const position = parsed.key ? keys.indexOf(parsed.key) : 0
    if (position >= 0) {
      setQueue({ keys, index: position })
      return
    }
    if (parsed.key) setQueue({ keys: [parsed.key], index: 0 })
  }, [board.board, queue, reviewSnapshot])

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Math.floor(Date.now() / 1000)), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    if (board.error?.status === 401) onSessionExpired()
  }, [board.error, onSessionExpired])

  const activeHasData = view === 'board' ? board.board !== null : backlog.hasLoaded

  useEffect(() => {
    if (reservedHeight === null || !activeHasData) return
    setReservedHeight(null)
  }, [reservedHeight, activeHasData])

  useEffect(() => {
    if (openKey) {
      lastOpenKey.current = openKey
      return
    }
    const key = lastOpenKey.current
    if (!key) return
    lastOpenKey.current = null
    document.getElementById(`open-${key}`)?.focus()
  }, [openKey])

  const filtered = Boolean(search.trim() || project)
  const needle = normalize(search.trim())

  const matches = useCallback(
    (task: Task) =>
      (!project || task.project === project) &&
      (!needle || normalize(`${task.key} ${task.title} ${task.project}`).includes(needle)),
    [needle, project],
  )

  const boardTasks = useMemo(() => board.tasks.filter(matches), [board.tasks, matches])
  const backlogRows = useMemo(() => backlog.rows.filter(matches), [backlog.rows, matches])

  const backlogCountLabel = backlog.hasLoaded
    ? backlog.nextCursor
      ? `${PAGE_SIZE}+`
      : String(backlog.rows.length)
    : '—'

  const titleCount =
    view === 'board'
      ? board.board
        ? String(boardTasks.length)
        : '—'
      : backlogCountLabel

  const tabCount =
    view === 'board'
      ? board.board
        ? board.board.counts.backlog
        : null
      : backlog.hasLoaded
        ? backlog.nextCursor
          ? PAGE_SIZE
          : backlog.rows.length
        : null

  const tabHasMore = view === 'backlog' && backlog.hasLoaded && Boolean(backlog.nextCursor)

  const summary =
    view === 'board'
      ? filtered
        ? `${boardTasks.length} of ${board.tasks.length} tasks`
        : 'Every move leaves a trace.'
      : filtered
        ? `${backlogRows.length} of ${backlogCountLabel} tasks`
        : 'The queue. Promote what comes next.'

  const activeError = view === 'board' ? board.error : backlog.error
  const activeReload = view === 'board' ? board.reload : backlog.reload
  const { projects, reload: reloadProjects } = useProjects(true)

  const clearFilters = useCallback(() => {
    setSearch('')
    setProject('')
    setOpenKey(null)
  }, [])

  async function create(input: CreateTaskInput, idempotencyKey: string) {
    try {
      const task = await api.createTask(input, idempotencyKey)
      setCreating(false)
      setOpenKey(null)
      setSearch('')
      setProject('')
      setCreatedTask(task)
      setNotice(`${task.key} added to the backlog`)
      if (viewRef.current !== 'backlog') {
        viewRef.current = 'backlog'
        setView('backlog')
      }
      if (location.hash !== '#backlog') location.hash = '#backlog'
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) onSessionExpired()
      throw caught
    }
  }

  async function move(task: Task, destination: State, reason: string) {
    try {
      if (viewRef.current === 'backlog') {
        await api.patchTask(task.key, { state: destination, reason: reason || undefined }, task.version)
        setNotice(`${task.key} moved to ${STATE_LABELS[destination]}. The movement was recorded.`)
        await backlog.reload()
      } else {
        await board.mutate(() =>
          api.patchTask(task.key, { state: destination, reason: reason || undefined }, task.version),
        )
        setNotice(`${task.key} moved to ${STATE_LABELS[destination]}. The movement was recorded.`)
      }
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) onSessionExpired()
      throw caught
    }
  }

  const sheetKey = queue ? queue.keys[queue.index] : openKey
  const queueEmpty = queue !== null && queue.index >= queue.keys.length
  const reviewQueue: ReviewQueue | undefined = queue
    ? {
        position: queue.index + 1,
        total: queue.keys.length,
        canPrev: queue.index > 0,
        canNext: queue.index < queue.keys.length - 1,
        onPrev: () => goTo(queue.index - 1),
        onNext: () => goTo(queue.index + 1),
        onExit: exitQueue,
        onApproved: advance,
      }
    : undefined

  return (
    <main className="contenedor principal" id="panel" tabIndex={-1}>
      <a
        className="saltar"
        href="#panel-content"
        onClick={event => {
          event.preventDefault()
          contentRef.current?.focus()
        }}
      >
        Skip to content
      </a>

      <div className="titulo-tablero">
        <h1>{view === 'board' ? 'Board' : 'Backlog'}</h1>
        <ViewTabs activeView={view} backlogCount={tabCount} hasMore={tabHasMore} />
        <span className="total dato">
          {titleCount} {titleCount === '1' ? 'task' : 'tasks'}
        </span>
        <div className="titulo-acciones">
          <button type="button" className="primario" onClick={() => setCreating(value => !value)}>
            {creating ? 'Close form' : 'New task'}
          </button>
        </div>
      </div>

      <div className="herramientas">
        <div className="campo-busqueda">
          <label htmlFor="search">Search tasks</label>
          <input
            id="search"
            name="q"
            type="search"
            autoComplete="off"
            placeholder="Title, project or key"
            value={search}
            onChange={event => {
              setSearch(event.target.value)
              setOpenKey(null)
            }}
          />
        </div>
        <div className="campo-proyecto">
          <label htmlFor="project">Project</label>
          <select
            id="project"
            name="project"
            value={project}
            onChange={event => {
              setProject(event.target.value)
              setOpenKey(null)
            }}
          >
            <option value="">All projects</option>
            {projects.map(item => (
              <option key={item.key} value={item.key}>
                {item.name}
              </option>
            ))}
          </select>
        </div>
        <button
          type="button"
          className="enlace-oscuro limpiar"
          aria-hidden={!filtered}
          tabIndex={filtered ? 0 : -1}
          disabled={!filtered}
          onClick={clearFilters}
        >
          Clear filters
        </button>
        <p className="resultado" role="status">
          {summary}
        </p>
      </div>

      <div
        ref={contentRef}
        id="panel-content"
        className="panel-content"
        tabIndex={-1}
        style={reservedHeight !== null ? { minHeight: reservedHeight } : undefined}
      >
        {activeError && (
          <div className="aviso error" role="alert">
            <span>{activeError.message}</span>
            <button type="button" className="secundario" onClick={() => void activeReload()}>
              Retry
            </button>
          </div>
        )}

        {notice && (
          <p className="aviso" role="status" aria-live="polite">
            {notice}
          </p>
        )}

        {creating && (
          <CreateTask
            projects={projects}
            onCreate={create}
            onCancel={() => setCreating(false)}
          />
        )}

        <div className="vista" hidden={view !== 'board'}>
          {board.board !== null ? (
            <Board
              tasks={boardTasks}
              details={board.details}
              now={now}
              openKey={openKey}
              filtered={filtered}
              onOpen={openCard}
              onClearFilters={clearFilters}
              onStartReview={startReview}
            />
          ) : (
            <p
              className="panel-cargando"
              role={board.loading ? 'status' : undefined}
              style={reservedHeight !== null ? { minHeight: reservedHeight } : undefined}
            >
              {board.loading ? 'Loading board…' : null}
            </p>
          )}
        </div>

        <div className="vista" hidden={view !== 'backlog'}>
          {backlog.hasLoaded ? (
            <Backlog
              rows={backlogRows}
              loadedCount={backlog.rows.length}
              flashKey={backlog.flashKey}
              onOpen={openCard}
              onClearFilters={clearFilters}
              onReload={backlog.reload}
              onProjectCreated={reloadProjects}
              onNoticeChange={setNotice}
              onSessionExpired={onSessionExpired}
            />
          ) : (
            <p
              className="panel-cargando"
              role={backlog.loading ? 'status' : undefined}
              style={reservedHeight !== null ? { minHeight: reservedHeight } : undefined}
            >
              {backlog.loading ? 'Loading backlog…' : null}
            </p>
          )}
        </div>
      </div>

      {queueEmpty ? (
        <EmptyQueue onClose={exitQueue} />
      ) : sheetKey ? (
        <TaskDetail
          key={sheetKey}
          taskKey={sheetKey}
          now={now}
          queue={reviewQueue}
          onClose={queue ? exitQueue : closeCard}
          onMove={move}
          onReloadBoard={activeReload}
          onSessionExpired={onSessionExpired}
        />
      ) : null}
    </main>
  )
}

function EmptyQueue({ onClose }: { onClose: () => void }) {
  const closeButton = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    closeButton.current?.focus()
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.stopPropagation()
      onClose()
    }
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const root = document.getElementById('root')
    const hadInert = root?.hasAttribute('inert') ?? false
    const hadHidden = root?.getAttribute('aria-hidden') ?? null
    root?.setAttribute('inert', '')
    root?.setAttribute('aria-hidden', 'true')
    document.addEventListener('keydown', onKey)
    return () => {
      document.body.style.overflow = overflow
      if (!hadInert) root?.removeAttribute('inert')
      if (hadHidden === null) root?.removeAttribute('aria-hidden')
      else if (root) root.setAttribute('aria-hidden', hadHidden)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose])

  return createPortal(
    <div className="ficha-capa">
      <article
        className="ficha-modal ficha-cola-vacia"
        role="dialog"
        aria-modal="true"
        aria-label="Review queue"
      >
        <p className="ficha-cola-cuenta" role="status">
          Nothing left to review.
        </p>
        <button ref={closeButton} type="button" className="primario" onClick={onClose}>
          Close
        </button>
      </article>
    </div>,
    document.body,
  )
}
