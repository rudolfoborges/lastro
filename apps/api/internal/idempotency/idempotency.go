package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const maxKeyLength = 200

var (
	ErrConflict   = errors.New("this idempotency key was already used with a different request")
	ErrInvalidKey = fmt.Errorf("idempotency key must have 1 to %d printable characters", maxKeyLength)
)

// Do runs fn at most once per (account, operation, key) and must be called
// inside the tenant transaction that performs the writes: the stored response
// commits or rolls back together with them, so a key is only ever recorded for
// work that actually happened. Concurrent calls with the same key wait for the
// first one to finish and then replay its response.
func Do[T any](ctx context.Context, q *tenantdb.Queries, accountID uuid.UUID, operation, key string, request any, fn func() (T, error)) (result T, replayed bool, err error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > maxKeyLength || strings.ContainsFunc(key, unicode.IsControl) {
		return result, false, ErrInvalidKey
	}
	hash, err := requestHash(request)
	if err != nil {
		return result, false, err
	}
	if err := q.LockIdempotencyKey(ctx, accountID.String()+":"+operation+":"+key); err != nil {
		return result, false, fmt.Errorf("lock idempotency key: %w", err)
	}

	stored, err := q.GetIdempotencyKey(ctx, tenantdb.GetIdempotencyKeyParams{AccountID: accountID, Operation: operation, Key: key})
	switch {
	case err == nil:
		if !bytes.Equal(stored.RequestHash, hash) {
			return result, false, ErrConflict
		}
		if err := json.Unmarshal(stored.Response, &result); err != nil {
			return result, false, fmt.Errorf("decode stored response: %w", err)
		}
		return result, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return result, false, fmt.Errorf("read idempotency key: %w", err)
	}

	result, err = fn()
	if err != nil {
		return result, false, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return result, false, fmt.Errorf("encode response: %w", err)
	}
	err = q.InsertIdempotencyKey(ctx, tenantdb.InsertIdempotencyKeyParams{
		AccountID:   accountID,
		Operation:   operation,
		Key:         key,
		RequestHash: hash,
		Response:    response,
	})
	if err != nil {
		return result, false, fmt.Errorf("record idempotency key: %w", err)
	}
	return result, false, nil
}

func requestHash(request any) ([]byte, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode request for idempotency: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}
