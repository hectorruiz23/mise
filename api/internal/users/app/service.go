package app

import (
	"context"
	"crypto/subtle"
	"errors"

	"mise/api/internal/platform/credential"
	"mise/api/internal/users/domain"
)

type Repo interface {
	CreateSignupRequest(ctx context.Context, email, secretHash string, at int64) (int64, error)
	FindPendingSignupByEmail(ctx context.Context, email string) (SignupRequest, bool, error)
	GetSignupRequest(ctx context.Context, id int64) (SignupRequest, error)
	ApproveSignupRequest(ctx context.Context, id int64, at int64) error
	ConsumeSignupRequest(ctx context.Context, id int64, userID int64, at int64) error
	ClaimSignupRequest(ctx context.Context, id int64, u domain.User, tokenHash string, at int64) (domain.User, *domain.RuleError, error)
	CreateUser(ctx context.Context, u domain.User, tokenHash string) (domain.User, error)
	GetUserByTokenHash(ctx context.Context, hash string) (domain.User, error)
	CreateSession(ctx context.Context, s domain.Session) error
	GetSession(ctx context.Context, idHash string) (domain.Session, domain.User, error)
	RevokeSession(ctx context.Context, idHash string, at int64) error
}

type Clock interface{ Now() int64 }

type SignupRequest struct {
	ID          int64
	Email       string
	SecretHash  string
	RequestedAt int64
	ApprovedAt  *int64
	ConsumedAt  *int64
	UserID      *int64
}

type Service struct {
	repo  Repo
	clock Clock
}

func New(repo Repo, clock Clock) *Service {
	return &Service{repo: repo, clock: clock}
}

func (s *Service) RequestSignup(ctx context.Context, email string) (id int64, secret string, err error) {
	normalized, err := domain.NormalizeEmail(email)
	if err != nil {
		return 0, "", err
	}
	secret, err = credential.NewSecret()
	if err != nil {
		return 0, "", err
	}
	secretHash := credential.Hash(secret)
	at := s.clock.Now()

	pending, found, err := s.repo.FindPendingSignupByEmail(ctx, normalized)
	if err != nil {
		return 0, "", err
	}
	if found {
		id, err = s.repo.CreateSignupRequest(ctx, pending.Email, secretHash, at)
		if err != nil {
			return 0, "", err
		}
		return id, secret, nil
	}

	id, err = s.repo.CreateSignupRequest(ctx, normalized, secretHash, at)
	if err != nil {
		if _, found, findErr := s.repo.FindPendingSignupByEmail(ctx, normalized); findErr == nil && found {
			if retryID, retryErr := s.repo.CreateSignupRequest(ctx, normalized, secretHash, at); retryErr == nil {
				return retryID, secret, nil
			}
		}
		return 0, "", err
	}
	return id, secret, nil
}

func (s *Service) SignupStatus(ctx context.Context, id int64, secret string) (string, error) {
	request, err := s.repo.GetSignupRequest(ctx, id)
	if err != nil {
		return "", err
	}
	if !secretMatches(secret, request.SecretHash) {
		return "", notFound("unknown signup request")
	}

	switch {
	case request.ConsumedAt != nil:
		return "consumed", nil
	case request.ApprovedAt != nil:
		return "approved", nil
	default:
		return "pending", nil
	}
}

func (s *Service) ClaimSignup(ctx context.Context, id int64, secret string) (domain.User, string, error) {
	request, err := s.repo.GetSignupRequest(ctx, id)
	if err != nil {
		return domain.User{}, "", err
	}
	if !secretMatches(secret, request.SecretHash) {
		return domain.User{}, "", notFound("unknown signup request")
	}

	token, err := credential.NewToken()
	if err != nil {
		return domain.User{}, "", err
	}
	now := s.clock.Now()
	user, err := domain.NewUser(
		request.Email,
		domain.NameFromEmail(request.Email),
		domain.Human,
		credential.Hash(token),
		now,
	)
	if err != nil {
		return domain.User{}, "", err
	}
	user, rule, err := s.repo.ClaimSignupRequest(ctx, id, user, credential.Hash(token), now)
	if err != nil {
		return domain.User{}, "", err
	}
	if rule != nil {
		return domain.User{}, "", rule
	}

	return user, token, nil
}

func (s *Service) Login(ctx context.Context, token string) (domain.Session, string, domain.User, error) {
	user, err := s.Authenticate(ctx, token)
	if err != nil {
		return domain.Session{}, "", domain.User{}, err
	}
	if user.Kind == domain.Agent {
		return domain.Session{}, "", domain.User{}, ruleError(
			domain.CodeForbidden, "an agent token cannot create a browser session")
	}

	cookieValue, err := credential.NewSecret()
	if err != nil {
		return domain.Session{}, "", domain.User{}, err
	}
	now := s.clock.Now()
	session, err := domain.NewSession(credential.Hash(cookieValue), user.ID, now)
	if err != nil {
		return domain.Session{}, "", domain.User{}, err
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return domain.Session{}, "", domain.User{}, err
	}
	return session, cookieValue, user, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (domain.User, error) {
	user, err := s.repo.GetUserByTokenHash(ctx, credential.Hash(token))
	if err != nil {
		var rule *domain.RuleError
		if errors.As(err, &rule) && rule.Code == domain.CodeNotFound {
			return domain.User{}, unauthorized("unknown token")
		}
		return domain.User{}, err
	}
	if !user.Active {
		return domain.User{}, unauthorized("user is inactive")
	}
	return user, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.repo.RevokeSession(ctx, credential.Hash(sessionID), s.clock.Now())
}

func (s *Service) SessionUser(ctx context.Context, sessionID string) (domain.User, error) {
	session, user, err := s.repo.GetSession(ctx, credential.Hash(sessionID))
	if err != nil {
		var rule *domain.RuleError
		if errors.As(err, &rule) && rule.Code == domain.CodeNotFound {
			return domain.User{}, unauthorized("unknown session")
		}
		return domain.User{}, err
	}
	if session.Revoked() {
		return domain.User{}, unauthorized("session was revoked")
	}
	if session.Expired(s.clock.Now()) {
		return domain.User{}, ruleError(domain.CodeSessionExpired, "session has expired")
	}
	if !user.Active {
		return domain.User{}, unauthorized("user is inactive")
	}
	return user, nil
}

func secretMatches(secret, expectedHash string) bool {
	actual := credential.Hash(secret)
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expectedHash)) == 1
}

func notFound(msg string) *domain.RuleError {
	return ruleError(domain.CodeNotFound, msg)
}

func unauthorized(msg string) *domain.RuleError {
	return ruleError(domain.CodeUnauthorized, msg)
}

func ruleError(code, msg string) *domain.RuleError {
	return &domain.RuleError{Code: code, Msg: msg}
}
