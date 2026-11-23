package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"

	"mise/api/internal/tasks/domain"
)

func commitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func planConflict(message string) error {
	return &domain.RuleError{Code: domain.CodeInvalidTransition, Msg: message}
}

func readyPlanTask(ctx context.Context, conn *sql.Conn, key string, actorID, now int64, reason string) error {
	var version int64
	err := conn.QueryRowContext(ctx, `UPDATE tasks SET state='ready', version=version+1, updated_at=?
		WHERE key=? AND state='backlog' RETURNING version`, now, key).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return planConflict("plan task is no longer waiting in backlog")
	}
	if err != nil {
		return err
	}
	from := domain.Backlog
	return insertMovement(ctx, conn, domain.Movement{TaskKey: key, FromState: &from, ToState: domain.Ready,
		ActorID: actorID, Reason: &reason, TaskVersion: version, At: now})
}
