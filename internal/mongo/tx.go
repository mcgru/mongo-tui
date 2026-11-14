package mongo

import (
	"context"
	"errors"
	"fmt"

	"mongo-tui/internal/engine"
)

func (s *Session) beginTransaction(ctx context.Context) (engine.Result, error) {
	s.txMu.Lock()
	defer s.txMu.Unlock()

	if s.tx != engine.TxIdle {
		return engine.Result{}, fmt.Errorf("mongo: transaction is already %s", s.tx)
	}
	if !s.replicaSet {
		return engine.Result{}, errors.New("mongo: transactions need a replica set; this deployment is standalone")
	}

	sess, err := s.client.StartSession()
	if err != nil {
		return engine.Result{}, fmt.Errorf("mongo: cannot start a session: %w", err)
	}
	if err := sess.StartTransaction(); err != nil {
		sess.EndSession(ctx)
		return engine.Result{}, fmt.Errorf("mongo: cannot begin a transaction: %w", err)
	}
	s.txSess = sess
	s.tx = engine.TxInTransaction
	return simpleResult(0, "transaction started"), nil
}

func (s *Session) commitTransaction(ctx context.Context) (engine.Result, error) {
	s.txMu.Lock()
	defer s.txMu.Unlock()

	if s.tx == engine.TxAborted {
		return engine.Result{Error: true, Messages: []string{"transaction is aborted; run abortTransaction"}}, nil
	}
	if s.tx != engine.TxInTransaction || s.txSess == nil {
		return engine.Result{}, errors.New("mongo: no transaction in progress")
	}

	err := s.txSess.CommitTransaction(ctx)
	s.txSess.EndSession(ctx)
	s.txSess = nil
	if err != nil {
		s.tx = engine.TxAborted
		return engine.Result{Error: true, Messages: []string{err.Error()}}, nil
	}
	s.tx = engine.TxIdle
	return simpleResult(0, "transaction committed"), nil
}

func (s *Session) abortTransaction(ctx context.Context) (engine.Result, error) {
	s.txMu.Lock()
	defer s.txMu.Unlock()

	if s.txSess == nil {
		s.tx = engine.TxIdle
		return simpleResult(0, "no transaction in progress"), nil
	}

	err := s.txSess.AbortTransaction(ctx)
	s.txSess.EndSession(ctx)
	s.txSess = nil
	s.tx = engine.TxIdle

	msg := "transaction aborted"
	if err != nil {
		msg += ": " + err.Error()
		return engine.Result{Error: true, Messages: []string{msg}}, nil
	}
	return simpleResult(0, msg), nil
}
