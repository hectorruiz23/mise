import { useState, type FormEvent } from 'react'
import { ApiError, newIdempotencyKey } from '../api'
import type { CreateTaskInput, Priority, Project, Size } from '../types'
import { PRIORITY_LABELS, SIZE_LABELS } from '../types'

export function CreateTask({
  projects,
  onCreate,
  onCancel,
}: {
  projects: Project[]
  onCreate: (input: CreateTaskInput, idempotencyKey: string) => Promise<void>
  onCancel: () => void
}) {
  const [title, setTitle] = useState('')
  const [notes, setNotes] = useState('')
  const [acceptance, setAcceptance] = useState('')
  const [project, setProject] = useState(projects[0]?.key ?? '')
  const [priority, setPriority] = useState<Priority>('normal')
  const [size, setSize] = useState<Size | ''>('')
  const [error, setError] = useState('')
  const [sending, setSending] = useState(false)
  const [idempotencyKey] = useState(newIdempotencyKey)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!title.trim()) {
      setError('Write a title.')
      return
    }
    if (!project) {
      setError('Choose a project.')
      return
    }

    setSending(true)
    setError('')
    try {
      await onCreate(
        {
          title: title.trim(),
          notes: notes.trim() ? notes : null,
          acceptance: acceptance.trim() ? acceptance : null,
          project,
          priority,
          size: size || null,
        },
        idempotencyKey,
      )
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : 'The task could not be created.')
    } finally {
      setSending(false)
    }
  }

  return (
    <section className="crear abertura" aria-labelledby="create-title">
      <h2 id="create-title">New task</h2>
      <form onSubmit={submit} noValidate>
        <label htmlFor="create-title-field">Title</label>
        <input
          id="create-title-field"
          name="title"
          type="text"
          required
          maxLength={200}
          autoFocus
          value={title}
          onChange={event => {
            setTitle(event.target.value)
            setError('')
          }}
        />

        <label htmlFor="create-notes">
          Notes <span className="opcional">Optional</span>
        </label>
        <textarea
          id="create-notes"
          name="notes"
          rows={3}
          maxLength={50000}
          value={notes}
          onChange={event => setNotes(event.target.value)}
        />

        <label htmlFor="create-acceptance">
          Acceptance <span className="opcional">Optional</span>
        </label>
        <textarea
          id="create-acceptance"
          name="acceptance"
          rows={3}
          maxLength={10000}
          value={acceptance}
          onChange={event => setAcceptance(event.target.value)}
        />

        <div className="campos-fila">
          <div>
            <label htmlFor="create-project">Project</label>
            <select
              id="create-project"
              name="project"
              required
              value={project}
              onChange={event => setProject(event.target.value)}
            >
              {projects.map(item => (
                <option key={item.key} value={item.key}>
                  {item.name} ({item.key})
                </option>
              ))}
            </select>
          </div>
          <div>
            <label htmlFor="create-priority">Priority</label>
            <select
              id="create-priority"
              name="priority"
              value={priority}
              onChange={event => setPriority(event.target.value as Priority)}
            >
              {(Object.keys(PRIORITY_LABELS) as Priority[]).map(value => (
                <option key={value} value={value}>
                  {PRIORITY_LABELS[value]}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label htmlFor="create-size">
              Size <span className="opcional">Optional</span>
            </label>
            <select
              id="create-size"
              name="size"
              value={size}
              onChange={event => setSize(event.target.value as Size | '')}
            >
              <option value="">No size</option>
              {(Object.keys(SIZE_LABELS) as Size[]).map(value => (
                <option key={value} value={value}>
                  {SIZE_LABELS[value]}
                </option>
              ))}
            </select>
          </div>
        </div>

        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}

        <div className="crear-actions">
          <button type="submit" className="primario" disabled={sending}>
            {sending ? 'Creating…' : 'Create task'}
          </button>
          <button type="button" className="secundario" onClick={onCancel} disabled={sending}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  )
}
