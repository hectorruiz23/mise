package app_test

import (
	"context"

	"mise/api/internal/users/app"
	"mise/api/internal/users/domain"
)

func (r *fakeRepo) FindPendingSignupByEmail(_ context.Context, email string) (app.SignupRequest, bool, error) {
	for _, request := range r.requests {
		if request.Email == email && request.ConsumedAt == nil {
			return request, true, nil
		}
	}
	return app.SignupRequest{}, false, nil
}

func (r *fakeRepo) ClaimSignupRequest(_ context.Context, id int64, u domain.User, tokenHash string, at int64) (domain.User, *domain.RuleError, error) {
	request, ok := r.requests[id]
	if !ok {
		return domain.User{}, &domain.RuleError{Code: domain.CodeNotFound, Msg: "unknown signup request"}, nil
	}
	if request.ApprovedAt == nil {
		return domain.User{}, &domain.RuleError{Code: domain.CodeSignupPending, Msg: "signup request is not approved"}, nil
	}
	if request.ConsumedAt != nil {
		return domain.User{}, &domain.RuleError{Code: domain.CodeSignupConsumed, Msg: "already consumed"}, nil
	}

	r.nextUser++
	u.ID = r.nextUser
	u.TokenHash = tokenHash
	r.users[u.ID] = u
	request.ConsumedAt = &at
	request.UserID = &u.ID
	r.requests[id] = request
	return u, nil, nil
}
