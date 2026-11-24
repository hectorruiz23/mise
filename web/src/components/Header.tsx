import { isViewer, type Me } from '../types'

export function Header({
  me,
  onSignIn,
  onSignOut,
}: {
  me: Me
  onSignIn: () => void
  onSignOut: () => void
}) {
  if (isViewer(me)) {
    return (
      <header className="cabecera">
        <div className="contenedor cabecera-interior">
          <a className="marca" href="/" aria-label="Mise home">
            mise<span aria-hidden="true">/</span>
          </a>
          <span className="nombre-espacio">Personal space</span>
          <span className="sesion">Read only</span>
          <button type="button" className="enlace-oscuro" onClick={onSignIn}>
            Sign in
          </button>
        </div>
      </header>
    )
  }

  return (
    <header className="cabecera">
      <div className="contenedor cabecera-interior">
        <a className="marca" href="/" aria-label="Mise home">
          mise<span aria-hidden="true">/</span>
        </a>
        <span className="nombre-espacio">Personal space</span>
        <span className="sesion">
          Signed in as <strong>{me.name}</strong>
        </span>
        <button type="button" className="enlace-oscuro" onClick={onSignOut}>
          Sign out
        </button>
      </div>
    </header>
  )
}
