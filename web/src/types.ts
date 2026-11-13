export const STATES = ['backlog', 'ready', 'in_progress', 'blocked', 'review', 'done'] as const

export type State = (typeof STATES)[number]
export type View = 'board' | 'backlog'
export type Priority = 'low' | 'normal' | 'high'
export type Size = 'S' | 'M' | 'L'

export const STATE_LABELS: Record<State, string> = {
  backlog: 'Backlog',
  ready: 'Ready to develop',
  in_progress: 'In progress',
  blocked: 'Blocked',
  review: 'Review',
  done: 'Done',
}

export const PRIORITY_LABELS: Record<Priority, string> = {
  low: 'Low',
  normal: 'Normal',
  high: 'High',
}

export const SIZE_LABELS: Record<Size, string> = {
  S: 'Small (S)',
  M: 'Medium (M)',
  L: 'Large (L)',
}

export type Task = {
  key: string
  project: string
  title: string
  notes: string | null
  acceptance: string | null
  state: State
  priority: Priority
  size: Size | null
  created_by: number
  version: number
  created_at: number
  updated_at: number
  closed_at: number | null
}

export type Project = { key: string; name: string }

export type Me = { id: number; name: string; kind: 'human' | 'agent'; email: string }

export type Actor = { id: number; name: string; kind: 'human' | 'agent' }

export function actorLabel(actor: Actor | null | undefined, fallbackId: number): string {
  const name = actor?.name?.trim()
  return name ? name : `#${actor?.id ?? fallbackId}`
}

export type BoardData = {
  columns: Record<State, Task[]>
  counts: Record<State, number>
  generated_at: number
}

export type Movement = {
  task_key: string
  from_state: State | null
  to_state: State
  actor_id: number
  actor: Actor
  reason: string | null
  task_version: number
  at: number
}

export type Lease = {
  id: number
  task_key: string
  agent_id: number
  agent: Actor
  created_at: number
  expires_at: number
  released_at: number | null
  closed_reason: string | null
}

export type Delivery = {
  id: number
  task_key: string
  lease_id: number | null
  summary: string
  evidence: string[]
  pr_url: string | null
  created_at: number
  actor_id: number
  actor: Actor
}

export type Comment = {
  id: number
  task_key: string
  actor_id: number
  actor: Actor
  body: string
  created_at: number
}

export type TaskDetail = {
  task: Task
  movements: Movement[]
  leases: Lease[]
  deliveries: Delivery[]
  comments: Comment[]
}

export type CreateTaskInput = {
  title: string
  notes: string | null
  acceptance: string | null
  project: string
  priority: Priority
  size: Size | null
}

export type TaskPatch = {
  state?: State
  reason?: string
}

export type SignupStatus = 'pending' | 'approved' | 'consumed'
