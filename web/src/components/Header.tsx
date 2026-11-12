import type { Me } from '../types'

export function Header({ user, onSignOut }: { user: Me; onSignOut: () => void }) {
  return (
    <header className="cabecera">
      <div className="contenedor cabecera-interior">
        <a className="marca" href="/" aria-label="Mise home">
          mise<span aria-hidden="true">/</span>
        </a>
        <span className="nombre-espacio">Personal space</span>
        <span className="sesion">
          Signed in as <strong>{user.name}</strong>
        </span>
        <button type="button" className="enlace-oscuro" onClick={onSignOut}>
          Sign out
        </button>
      </div>
    </header>
  )
}
