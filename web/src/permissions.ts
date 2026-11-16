import { isViewer, type Me, type State, type Task, type TaskDetail } from './types'

export type Capabilities = {
  canWrite: boolean
  canCreate: boolean
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
  canMove: true,
  canComment: true,
  canDeliver: true,
  canReview: true,
  canSeeBacklog: true,
  canSignOut: true,
}

const AGENT_CAPABILITIES: Capabilities = {
  canWrite: true,
  canCreate: false,
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

export function capabilitiesFor(me: Me): Capabilities {
  if (isViewer(me)) return VISITOR_CAPABILITIES
  if (me.kind === 'agent') return AGENT_CAPABILITIES
  return HUMAN_CAPABILITIES
}

export function destinationsFor(me: Me, task: Task, detail: TaskDetail | undefined): State[] {
  if (!capabilitiesFor(me).canWrite) return []

  const moves = HUMAN_MOVES[task.state] ?? []
  if (task.state === 'in_progress' && detail && detail.deliveries.length === 0) {
    return moves.filter(state => state !== 'review')
  }
  return moves
}

export function requiresReason(from: State, to: State): boolean {
  return from === 'blocked' || to === 'blocked'
}
