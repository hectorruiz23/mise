import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from './api'
import type { Me } from './types'
import { Header } from './components/Header'
import { Login } from './components/Login'
import { Panel } from './components/Panel'
import { Signup } from './components/Signup'

export default function App() {
  const [checking, setChecking] = useState(true)
  const [user, setUser] = useState<Me | null>(null)
  const [screen, setScreen] = useState<'login' | 'signup'>('login')
  const [authNotice, setAuthNotice] = useState('')

  useEffect(() => {
    let current = true
    api
      .me()
      .then(me => {
        if (current) setUser(me)
      })
      .catch(caught => {
        if (!current) return
        if (!(caught instanceof ApiError && caught.status === 401)) {
          setAuthNotice(caught instanceof ApiError ? caught.message : 'Cannot reach the server.')
        }
      })
      .finally(() => {
        if (current) setChecking(false)
      })
    return () => {
      current = false
    }
  }, [])

  const sessionExpired = useCallback(() => {
    setUser(null)
    setScreen('login')
    setAuthNotice('Your session expired. Sign in again.')
  }, [])

  async function signOut() {
    try {
      await api.logout()
    } catch {
    }
    setUser(null)
    setScreen('login')
    setAuthNotice('Signed out.')
  }

  if (checking) {
    return (
      <main className="contenedor principal">
        <p role="status">Checking session…</p>
      </main>
    )
  }

  if (!user) {
    if (screen === 'signup') {
      return <Signup onBack={() => setScreen('login')} />
    }
    return (
      <Login
        key={authNotice}
        initialError={authNotice}
        onSignedIn={me => {
          setUser(me)
          setAuthNotice('')
        }}
        onRequestAccess={() => {
          setScreen('signup')
          setAuthNotice('')
        }}
      />
    )
  }

  return (
    <>
      <Header user={user} onSignOut={() => void signOut()} />
      <Panel onSessionExpired={sessionExpired} />
    </>
  )
}
