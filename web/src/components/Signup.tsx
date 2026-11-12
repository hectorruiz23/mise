import { useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import type { SignupStatus } from '../types'

export function Signup({ onBack }: { onBack: () => void }) {
  const [email, setEmail] = useState('')
  const [requestID, setRequestID] = useState<number | null>(null)
  const [secret, setSecret] = useState('')
  const [status, setStatus] = useState<SignupStatus | ''>('')
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function requestAccess(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')
    setToken('')
    setStatus('')
    try {
      const result = await api.requestSignup(email.trim())
      setRequestID(result.id)
      setSecret(result.secret)
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : 'The request failed. Try again.')
    } finally {
      setBusy(false)
    }
  }

  async function checkStatus() {
    if (requestID === null || !secret) return
    setBusy(true)
    setError('')
    try {
      const result = await api.signupStatus(requestID, secret)
      setStatus(result.status)
      if (result.status === 'approved') {
        const claimed = await api.claimSignup(requestID, secret)
        setToken(claimed.token)
        setStatus('consumed')
      }
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : 'The check failed. Try again.')
    } finally {
      setBusy(false)
    }
  }

  const statusMessage =
    status === 'pending'
      ? 'Pending approval. Check again after the owner approves this address.'
      : status === 'approved'
        ? 'Approved.'
        : status === 'consumed'
          ? token
            ? 'Approved and claimed. This token is shown once.'
            : 'This request was already consumed. If the token was lost, ask the owner to rotate it.'
          : ''

  return (
    <main className="contenedor principal">
      <div className="access">
        <section className="access-panel" aria-labelledby="signup-title">
          <div className="marca access-brand" aria-hidden="true">
            mise<span>/</span>
          </div>
          <h1 id="signup-title">Request access</h1>
          <p>
            Ask for a signup request with your email. The owner approves it by hand before you can
            claim a token.
          </p>
          <form onSubmit={requestAccess} noValidate>
            <label htmlFor="signup-email">Email</label>
            <input
              id="signup-email"
              name="email"
              type="email"
              autoComplete="email"
              autoFocus
              required
              value={email}
              onChange={event => {
                setEmail(event.target.value)
                setError('')
              }}
            />
            {error && (
              <p className="error" role="alert">
                {error}
              </p>
            )}
            <div className="access-actions">
              <button type="submit" className="primario" disabled={busy || email.trim() === ''}>
                {busy && requestID === null ? 'Sending…' : 'Request access'}
              </button>
              <button type="button" className="access-link" onClick={onBack}>
                Back to sign in
              </button>
            </div>
          </form>

          {requestID !== null && secret && (
            <div className="access-secret">
              <p>
                Request <strong className="dato">#{requestID}</strong> created.
              </p>
              <p>Your signup secret:</p>
              <code>{secret}</code>
              <p className="access-warning">
                Save the secret now. It is shown once and is required to check the status and claim
                the token.
              </p>
            </div>
          )}

          {requestID !== null && secret && (
            <div className="access-actions">
              <button type="button" className="secundario" onClick={checkStatus} disabled={busy}>
                {busy ? 'Checking…' : 'Check status'}
              </button>
            </div>
          )}

          {statusMessage && (
            <p role="status" aria-live="polite">
              {statusMessage}
            </p>
          )}

          {token && (
            <div className="access-secret">
              <p>Your access token:</p>
              <code>{token}</code>
              <p className="access-warning">
                This token is shown once. Store it before leaving this page; use it to sign in.
              </p>
            </div>
          )}
        </section>
      </div>
    </main>
  )
}
