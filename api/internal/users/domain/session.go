package domain

const SessionTTLSeconds int64 = 30 * 24 * 3600

type Session struct {
	ID        string
	UserID    int64
	CreatedAt int64
	ExpiresAt int64
	RevokedAt *int64
}

func (s Session) Revoked() bool {
	return s.RevokedAt != nil
}

func (s Session) Expired(now int64) bool {
	return s.ExpiresAt <= now
}

func (s Session) Valid(now int64) bool {
	return !s.Revoked() && !s.Expired(now)
}

func NewSession(idHash string, userID, at int64) (Session, error) {
	if !isHex64(idHash) {
		return Session{}, invalidInput("session id must be 64 hexadecimal characters")
	}
	if userID <= 0 {
		return Session{}, invalidInput("user id is required")
	}
	return Session{
		ID:        idHash,
		UserID:    userID,
		CreatedAt: at,
		ExpiresAt: at + SessionTTLSeconds,
	}, nil
}

func isHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
