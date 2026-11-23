export const STATES = ['backlog', 'ready', 'in_progress', 'blocked', 'review', 'done'] as const

export type State = (typeof STATES)[number]
export type View = 'board' | 'backlog' | 'done'
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

export type AuthenticatedMe = {
  id: number
  name: string
  kind: 'human' | 'agent'
  email: string
}

export type ViewerMe = {
  id: 0
  name: string
  kind: 'viewer'
  email: ''
}

export type Me = AuthenticatedMe | ViewerMe

export function isViewer(me: Me): me is ViewerMe {
  return me.kind === 'viewer'
}

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

export type EvidenceItem = {
  label: string | null
  raw: string
}

export type Delivery = {
  id: number
  task_key: string
  lease_id: number | null
  summary: string
  evidence: string[]
  evidence_items?: EvidenceItem[]
  pr_url: string | null
  created_at: number
  actor_id: number
  actor: Actor
}

export type EnvelopeStatus = 'fresh' | 'stale' | 'unavailable' | 'unknown'

export type Envelope<T> = {
  status: EnvelopeStatus
  data: T
  checked_at: number | null
  expires_at: number | null
  reason: string | null
  retry_at: number | null
}

export type PullRequestChecks = {
  sha: string
  context: 'pull_request' | 'push' | 'unknown'
  run_id: number | null
  attempt: number | null
  jobs: { name: string; status: string; conclusion: string | null; url: string | null }[]
  result: 'success' | 'failure' | 'pending' | 'unknown'
}

export type PullRequestAnchor =
  | { kind: 'link'; url: string }
  | {
      kind: 'github'
      url: string
      state: Envelope<string>
      merge_sha: Envelope<string | null>
      checks: Envelope<PullRequestChecks | null>
    }

export type Anchors = {
  task_key: string
  delivery_id: number | null
  pr: PullRequestAnchor | null
  main: Envelope<string | null>
}

export type BuildInfo = {
  repository: string | null
  revision: string | null
  tree_state: 'clean' | 'dirty' | 'unknown'
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

export type DraftTask = {
  key: string
  title: string
  notes: string | null
  acceptance: string | null
  size: Size | null
  task_key: string | null
}

export type DraftEdge = {
  from: string
  to: string
  kind: 'integration' | 'deployment'
}

export type PlanDraft = {
  id: number
  project: string
  author_id: number
  policy: { repository: string; branch: string; workflow_id: number }
  outcome: string
  scenario: string
  target_environment: string
  visible_change: string | null
  visual_review: boolean
  reveal_key: string | null
  version: number
  state: 'draft' | 'approved' | 'discarded'
  approved_by: number | null
  approved_at: number | null
  plan_id: number | null
  tasks: DraftTask[]
  edges: DraftEdge[]
  roots: string[]
  created_at: number
  updated_at: number
}

export type PlanDraftSummary = {
  id: number
  project: string
  version: number
  state: 'draft' | 'approved' | 'discarded'
  plan_id: number | null
}

export type PlanPolicy = {
  repository: string
  branch: string
  workflow_id: number
}

export type PlanProof = {
  task_key: string
  delivery_id: number
  pr_url: string
  head_sha: string
  merge_sha: string
  run_id: number
  attempt: number
  verified_at: number
  origin: string
}

export type PlanDeployment = {
  environment: string
  revision: string
  observed_at: number
}

export type PlanApproval = {
  task_key: string
  head_sha: string
  reviewer: string
  approved_at: number
}

export type PlanEdge = {
  From: string
  To: string
  Kind: 'integration' | 'deployment'
}

export type PlanNode = {
  task_key: string
  title: string
  state: State
  acceptance: string | null
  waiting_on: string[]
  proof: PlanProof | null
  deployments: PlanDeployment[]
  reveals: boolean
  correction: boolean
  origin_evidence: string | null
}

export type PlanGraph = {
  id: number
  project: string
  policy: PlanPolicy
  scenario: string
  visible_change: string | null
  visual_review: boolean
  target_environment?: string
  approved_by: number
  approved_at: number
  completed_at: number | null
  state: 'active' | 'completed' | 'cancelled'
  cancelled_by?: number
  cancelled_at?: number
  approval: PlanApproval | null
  nodes: PlanNode[]
  edges: PlanEdge[]
}
