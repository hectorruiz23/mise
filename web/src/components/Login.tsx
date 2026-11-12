import { useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import type { Me } from '../types'

export function Login({
  onSignedIn,
  onRequestAccess,
  initialError = '',
}: {
  onSignedIn: (user: Me) => void
  onRequestAccess: () => void
  initialError?: string
}) {
  const [token, setToken] = useState('')
  const [error, setError] = useState(initialError)
  const [sending, setSending] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const value = token.trim()
    if (!value) {
      setError('Paste your access token first.')
      return
    }

    setSending(true)
    setError('')
    try {
      onSignedIn(await api.login(value))
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) {
        setError('That token is not valid.')
      } else if (caught instanceof ApiError) {
        setError(caught.message)
      } else {
        setError('Sign in failed. Try again.')
      }
    } finally {
      setSending(false)
    }
  }

  return (
    <main className="contenedor principal">
      <div className="access">
        <section className="access-panel" aria-labelledby="login-title">
          <div className="marca access-brand" aria-hidden="true">
            mise<span>/</span>
          </div>
          <h1 id="login-title">Sign in</h1>
          <p>Paste a human access token. The browser session lasts 30 days.</p>
          <form onSubmit={submit} noValidate>
            <label htmlFor="token">Access token</label>
            <input
              id="token"
              name="token"
              type="password"
              autoComplete="current-password"
              autoFocus
              required
              value={token}
              onChange={event => {
                setToken(event.target.value)
                setError('')
              }}
            />
            {error && (
              <p className="error" role="alert">
                {error}
              </p>
            )}
            <div className="access-actions">
              <button type="submit" className="primario" disabled={sending || token.trim() === ''}>
                {sending ? 'Signing in…' : 'Sign in'}
              </button>
              <button type="button" className="access-link" onClick={onRequestAccess}>
                Request access
              </button>
            </div>
          </form>
        </section>
      </div>
    </main>
  )
}
