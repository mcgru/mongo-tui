package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	mg "go.mongodb.org/mongo-driver/v2/mongo"
)

// Cancel stops the operation currently running in this session.
//
// The request carries a unique comment; Cancel finds it in $currentOp and calls
// killOp. Cancelling the context is not a substitute: it closes the client
// connection, returns a transport error, and leaves the server operation
// running (measured in docs/spike-mongo.md).
func (s *Session) Cancel(ctx context.Context) error {
	comment := s.getComment()
	if comment == "" {
		return errors.New("mongo: no request is running")
	}
	opid, err := s.currentOpID(ctx, comment)
	if err != nil {
		return err
	}
	return s.killOp(ctx, opid)
}

func (s *Session) currentOpID(ctx context.Context, comment string) (int64, error) {
	var lastErr error
	for _, allUsers := range []bool{true, false} {
		pipeline := mg.Pipeline{
			{e("$currentOp", d(e("allUsers", allUsers), e("idleConnections", false)))},
			{e("$match", d(e("command.comment", comment)))},
			{e("$limit", 1)},
		}
		cur, err := s.client.Database("admin").Aggregate(ctx, pipeline)
		if err != nil {
			lastErr = err
			continue
		}
		var ops []bson.M
		err = cur.All(ctx, &ops)
		_ = cur.Close(context.Background())
		if err != nil {
			lastErr = err
			continue
		}
		if len(ops) > 0 {
			return int64(numberOf(ops[0]["opid"])), nil
		}
		// The query itself succeeded; the absence of a match is the answer.
		lastErr = nil
	}
	if lastErr != nil {
		return 0, fmt.Errorf("mongo: cannot inspect current operations: %w", lastErr)
	}
	return 0, fmt.Errorf("mongo: no operation with comment %q, it may have finished", comment)
}

func (s *Session) killOp(ctx context.Context, opid int64) error {
	var out bson.M
	if err := s.client.Database("admin").
		RunCommand(ctx, d(e("killOp", 1), e("op", opid))).
		Decode(&out); err != nil {
		return err
	}
	if numberOf(out["ok"]) != 1 {
		return fmt.Errorf("mongo: killOp returned %v", out)
	}
	return nil
}

func numberOf(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
