import { useState, type FormEvent } from 'react'
import { formatDateTime, formatRelative, isoDate } from '../dates'
import type { Comment } from '../types'
import { actorLabel } from '../types'

export function Comments({
  comments,
  now,
  onPost,
}: {
  comments: Comment[]
  now: number
  onPost: (body: string) => Promise<void>
}) {
  const [body, setBody] = useState('')
  const [error, setError] = useState('')
  const [sending, setSending] = useState(false)

  const ordered = [...comments].sort((a, b) => a.created_at - b.created_at || a.id - b.id)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!body.trim()) {
      setError('Write a comment before posting.')
      return
    }

    setSending(true)
    setError('')
    try {
      await onPost(body.trim())
      setBody('')
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The comment could not be posted.')
    } finally {
      setSending(false)
    }
  }

  return (
    <section className="sheet-section" aria-labelledby="comments-heading">
      <div className="sheet-section-head">
        <h3 id="comments-heading">Comments</h3>
        <span className="dato">{ordered.length}</span>
      </div>
      {ordered.length === 0 ? (
        <p className="sheet-empty">No comments yet. Notes from you and the agent appear here.</p>
      ) : (
        <ol className="comment-list">
          {ordered.map(comment => {
            const agent = comment.actor?.kind === 'agent'
            return (
              <li key={comment.id} className={`comment${agent ? ' comment-agent' : ''}`}>
                <p className="comment-head">
                  <strong>{actorLabel(comment.actor, comment.actor_id)}</strong>
                  {agent && <span className="actor-badge">Agent</span>}
                  <span className="dato comment-date">
                    <time dateTime={isoDate(comment.created_at)}>
                      {formatDateTime(comment.created_at)}
                    </time>{' '}
                    <span className="relative">({formatRelative(comment.created_at, now)})</span>
                  </span>
                </p>
                <p className="comment-body">{comment.body}</p>
              </li>
            )
          })}
        </ol>
      )}

      <form className="sheet-form comment-form" onSubmit={submit} noValidate>
        <label htmlFor="comment-body">
          Add a comment
          <span className="opcional">Plain text. The author comes from your credential.</span>
        </label>
        <textarea
          id="comment-body"
          name="body"
          rows={3}
          maxLength={10000}
          value={body}
          disabled={sending}
          aria-invalid={Boolean(error)}
          aria-describedby={error ? 'comment-error' : undefined}
          onChange={event => {
            setBody(event.target.value)
            setError('')
          }}
        />
        {error && (
          <p id="comment-error" className="error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" className="primario" disabled={sending || !body.trim()}>
          {sending ? 'Posting…' : 'Post comment'}
        </button>
      </form>
    </section>
  )
}
