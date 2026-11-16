import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'
import { isViewer, type Me } from './types'
import { Header } from './components/Header'
import { Login } from './components/Login'
import { Panel } from './components/Panel'
import { Signup } from './components/Signup'

type Screen = 'panel' | 'login' | 'signup'

export default function App() {
  const [checking, setChecking] = useState(true)
  const [error, setError] = useState('')
  const [me, setMe] = useState<Me | null>(null)
  const [screen, setScreen] = useState<Screen>('panel')
  const [notice, setNotice] = useState('')
  const attempt = useRef(0)

  const loadMe = useCallback(async () => {
    const current = ++attempt.current
    setChecking(true)
    setError('')
    try {
      const next = await api.me()
      if (current !== attempt.current) return
      setMe(next)
    } catch (caught) {
      if (current !== attempt.current) return
      setMe(null)
      if (caught instanceof ApiError && caught.status === 401) {
        setScreen('login')
        setNotice('Your session expired. Sign in again.')
      } else {
        setError(caught instanceof ApiError ? caught.message : 'Cannot reach the server.')
      }
    } finally {
      if (current === attempt.current) setChecking(false)
    }
  }, [])

  useEffect(() => {
    void loadMe()
  }, [loadMe])

  const sessionExpired = useCallback(() => {
    setNotice('Your session expired. Sign in again.')
    setScreen('panel')
    void loadMe()
  }, [loadMe])

  async function signOut() {
    try {
      await api.logout()
    } catch {
    }
    setNotice('Signed out.')
    setScreen('panel')
    await loadMe()
  }

  if (checking) {
    return (
      <main className="contenedor principal">
        <p role="status">Checking session…</p>
      </main>
    )
  }

  if (error) {
    return (
      <main className="contenedor principal">
        <p className="error" role="alert">
          {error}
        </p>
        <button type="button" className="secundario" onClick={() => void loadMe()}>
          Retry
        </button>
      </main>
    )
  }

  if (screen === 'signup') {
    return <Signup onBack={() => setScreen('login')} />
  }

  if (screen === 'login') {
    return (
      <Login
        initialError={notice}
        onSignedIn={() => {
          setNotice('')
          setScreen('panel')
          void loadMe()
        }}
        onRequestAccess={() => {
          setScreen('signup')
          setNotice('')
        }}
        onBack={() => setScreen('panel')}
      />
    )
  }

  if (!me) {
    return (
      <main className="contenedor principal">
        <p className="error" role="alert">
          Cannot reach the server.
        </p>
        <button type="button" className="secundario" onClick={() => void loadMe()}>
          Retry
        </button>
      </main>
    )
  }

  const panelKey = isViewer(me) ? 'viewer' : `${me.kind}-${me.id}`

  return (
    <>
      <Header
        me={me}
        onSignIn={() => setScreen('login')}
        onSignOut={() => void signOut()}
      />
      <Panel key={panelKey} onSessionExpired={sessionExpired} />
    </>
  )
}
