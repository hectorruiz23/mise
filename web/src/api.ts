import type {
  BoardData,
  Comment,
  CreateTaskInput,
  Delivery,
  Lease,
  Me,
  Movement,
  Project,
  SignupStatus,
  State,
  Task,
  TaskDetail,
  TaskPatch,
} from './types'

export type ListTasksParams = {
  state?: State
  project?: string
  limit?: number
}

export type ListTasksResponse = {
  tasks: Task[]
  next_cursor: string
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    public readonly detail: string,
  ) {
    super(messageFor(status, code, detail))
    this.name = 'ApiError'
  }
}

const MESSAGES: Record<string, string> = {
  invalid_input: 'Some fields are not valid.',
  unauthorized: 'Your token is not valid, or your session has expired.',
  session_expired: 'Your session has expired. Sign in again.',
  forbidden: 'You are not allowed to do that.',
  not_found: 'That item does not exist.',
  precondition_required: 'A required precondition is missing.',
  stale_version: 'This card changed, reloading.',
  invalid_transition: 'The API does not allow that move from the current state.',
  reason_required: 'Write a reason before moving a blocked task in or out.',
  evidence_required: 'A delivery is required before a task can move to review.',
  lease_required: 'This action needs a live lease owned by the actor.',
  lease_conflict: 'Another actor already holds a live lease on this task.',
  idempotency_conflict: 'This retry reused the key with a different payload.',
  signup_pending: 'This signup request is still pending approval.',
  signup_consumed: 'This signup request has already been used.',
  rate_limited: 'Too many attempts. Wait a few minutes and try again.',
  payload_too_large: 'The request is too large.',
  method_not_allowed: 'That method is not allowed here.',
  unsupported_media_type: 'The request body must be JSON.',
  internal: 'The server could not complete the request.',
}

function messageFor(status: number, code: string, detail: string): string {
  if (MESSAGES[code]) return MESSAGES[code]
  if (detail.trim()) return detail
  return `The request failed with status ${status}.`
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body !== undefined && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  let response: Response
  try {
    response = await fetch(path, { ...init, headers, credentials: 'include' })
  } catch {
    throw new ApiError(0, 'network', 'Cannot reach the server.')
  }

  if (!response.ok) {
    let code = ''
    let detail = ''
    try {
      const body = (await response.json()) as { error?: string; detail?: string }
      code = body.error ?? ''
      detail = body.detail ?? ''
    } catch {
    }
    throw new ApiError(response.status, code || 'http', detail)
  }

  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

export function newIdempotencyKey(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
}

export const api = {
  me: () => request<Me>('/api/me'),

  login: (token: string) =>
    request<Me>('/api/sessions', {
      method: 'POST',
      body: JSON.stringify({ token }),
    }),

  logout: () => request<void>('/api/sessions/current', { method: 'DELETE' }),

  projects: () => request<Project[]>('/api/projects'),

  createProject: (input: { key: string; name: string }) =>
    request<Project>('/api/projects', { method: 'POST', body: JSON.stringify(input) }),

  board: (project = '') =>
    request<BoardData>(`/api/board${project ? `?project=${encodeURIComponent(project)}` : ''}`),

  listTasks: (params: ListTasksParams) => {
    const query = new URLSearchParams()
    if (params.state) query.set('state', params.state)
    if (params.project) query.set('project', params.project)
    if (params.limit !== undefined) query.set('limit', String(params.limit))
    const suffix = query.toString()
    return request<ListTasksResponse>(`/api/tasks${suffix ? `?${suffix}` : ''}`)
  },

  detail: (key: string) => request<TaskDetail>(`/api/tasks/${encodeURIComponent(key)}`),

  movements: (key: string) =>
    request<Movement[]>(`/api/tasks/${encodeURIComponent(key)}/movements`),

  deliveries: (key: string) =>
    request<Delivery[]>(`/api/tasks/${encodeURIComponent(key)}/deliveries`),

  leases: (key: string) => request<Lease[]>(`/api/tasks/${encodeURIComponent(key)}/leases`),

  comments: (key: string) =>
    request<Comment[]>(`/api/tasks/${encodeURIComponent(key)}/comments`),

  postComment: (key: string, body: string) =>
    request<Comment>(`/api/tasks/${encodeURIComponent(key)}/comments`, {
      method: 'POST',
      body: JSON.stringify({ body }),
    }),

  createTask: (input: CreateTaskInput, idempotencyKey: string) =>
    request<Task>('/api/tasks', {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
      body: JSON.stringify(input),
    }),

  patchTask: (key: string, patch: TaskPatch, version: number) =>
    request<Task>(`/api/tasks/${encodeURIComponent(key)}`, {
      method: 'PATCH',
      headers: { 'If-Match': `"${version}"` },
      body: JSON.stringify(patch),
    }),

  requestSignup: (email: string) =>
    request<{ id: number; secret: string }>('/api/signup-requests', {
      method: 'POST',
      body: JSON.stringify({ email }),
    }),

  signupStatus: (id: number, secret: string) =>
    request<{ status: SignupStatus }>(
      `/api/signup-requests/${id}?secret=${encodeURIComponent(secret)}`,
    ),

  claimSignup: (id: number, secret: string) =>
    request<{ user_id: number; token: string }>(`/api/signup-requests/${id}/claim`, {
      method: 'POST',
      body: JSON.stringify({ secret }),
    }),
}
