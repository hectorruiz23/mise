import { useState, type FormEvent } from 'react'
import { api } from '../api'
import type { Project } from '../types'

export function ProjectGitHub({ project, onSaved }: { project: Project; onSaved: () => Promise<void> }) {
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const fields = new FormData(event.currentTarget)
    setSaving(true)
    setMessage('')
    try {
      await api.updateGitHub(project.key, {
        github: {
          repository: String(fields.get('repository')).trim(),
          branch: String(fields.get('branch')).trim(),
          workflow_id: Number(fields.get('workflow')),
        },
        auto_merge: fields.has('merge'),
      })
      await onSaved()
      setMessage('GitHub settings saved.')
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not save settings.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <details>
      <summary>GitHub settings for {project.name}</summary>
      <form onSubmit={save} onChange={() => setMessage('')}>
        <fieldset disabled={saving}>
          <label>Repository <input name="repository" required placeholder="owner/repository" defaultValue={project.github.repository} /></label>
          <label>Target branch <input name="branch" required defaultValue={project.github.branch} /></label>
          <label>Workflow ID <input name="workflow" type="number" min="1" required defaultValue={project.github.workflow_id || ''} /></label>
          <label><input name="merge" type="checkbox" defaultChecked={project.auto_merge} /> Auto-merge</label>
          <button type="submit">Save settings</button>
        </fieldset>
        <p role="status">{message}</p>
      </form>
    </details>
  )
}
