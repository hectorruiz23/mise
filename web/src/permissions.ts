import { isViewer, type Me, type State, type Task, type TaskDetail, type View } from './types'

export type Capabilities = {
  canWrite: boolean
  canCreate: boolean
  canPromoteFromBacklog: boolean
  canMove: boolean
  canComment: boolean
  canDeliver: boolean
  canReview: boolean
  canSeeBacklog: boolean
  canSignOut: boolean
}

const VISITOR_CAPABILITIES: Capabilities = {
  canWrite: false,
  canCreate: false,
  canPromoteFromBacklog: false,
  canMove: false,
  canComment: false,
  canDeliver: false,
  canReview: false,
  canSeeBacklog: false,
  canSignOut: false,
}

const HUMAN_CAPABILITIES: Capabilities = {
  canWrite: true,
  canCreate: true,
  canPromoteFromBacklog: true,
  canMove: true,
  canComment: true,
  canDeliver: true,
  canReview: true,
  canSeeBacklog: true,
  canSignOut: true,
}

const AGENT_CAPABILITIES: Capabilities = {
  canWrite: true,
  canCreate: true,
  canPromoteFromBacklog: false,
  canMove: true,
  canComment: true,
  canDeliver: true,
  canReview: false,
  canSeeBacklog: true,
  canSignOut: true,
}

const HUMAN_MOVES: Record<State, State[]> = {
  backlog: ['ready'],
  ready: ['backlog', 'in_progress'],
  in_progress: ['ready', 'blocked', 'review'],
  blocked: ['ready', 'in_progress'],
  review: ['in_progress', 'done'],
  done: ['ready'],
}

const AGENT_MOVES: Record<State, State[]> = {
  backlog: [],
  ready: ['in_progress'],
  in_progress: ['blocked', 'review'],
  blocked: ['in_progress'],
  review: ['in_progress'],
  done: [],
}

export function capabilitiesFor(me: Me): Capabilities {
  if (isViewer(me)) return VISITOR_CAPABILITIES
  if (me.kind === 'agent') return AGENT_CAPABILITIES
  return HUMAN_CAPABILITIES
}

export function canVisit(caps: Capabilities, view: View): boolean {
  if (view === 'backlog') return caps.canSeeBacklog
  return true
}

export function destinationsFor(me: Me, task: Task, detail: TaskDetail | undefined): State[] {
  if (!capabilitiesFor(me).canWrite) return []

  const moves = (me.kind === 'agent' ? AGENT_MOVES : HUMAN_MOVES)[task.state] ?? []
  if (task.state === 'in_progress' && detail && detail.deliveries.length === 0) {
    return moves.filter(state => state !== 'review')
  }
  return moves
}

export function requiresReason(from: State, to: State): boolean {
  return from === 'blocked' || to === 'blocked'
}
