import type { State, Task, TaskDetail } from './types'

const HUMAN_MOVES: Record<State, State[]> = {
  backlog: ['ready'],
  ready: ['backlog', 'in_progress'],
  in_progress: ['ready', 'blocked', 'review'],
  blocked: ['ready', 'in_progress'],
  review: ['in_progress', 'done'],
  done: ['ready'],
}

export function destinationsFor(task: Task, detail: TaskDetail | undefined): State[] {
  const moves = HUMAN_MOVES[task.state] ?? []
  if (task.state === 'in_progress' && detail && detail.deliveries.length === 0) {
    return moves.filter(state => state !== 'review')
  }
  return moves
}

export function requiresReason(from: State, to: State): boolean {
  return from === 'blocked' || to === 'blocked'
}
