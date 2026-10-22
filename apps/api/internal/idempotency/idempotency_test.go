package idempotency_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/idempotency"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

type fixture struct {
	t           *testing.T
	store       *store.Store
	databaseURL string
	account     uuid.UUID
	calls       atomic.Int32
}

type request struct {
	Text   string `json:"text"`
	Amount string `json:"amount"`
}

type response struct {
	ObservationID uuid.UUID `json:"observation_id"`
}

var errMidway = errors.New("failure after the write")

func newFixture(t *testing.T) *fixture {
	t.Helper()
	databaseURL := storetest.MigratedDatabaseURL(t)
	s, err := store.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	f := &fixture{t: t, store: s, databaseURL: databaseURL}
	f.account = f.newAccount()
	return f
}

func (f *fixture) newAccount() uuid.UUID {
	f.t.Helper()
	account, err := f.store.Platform().CreateAccount(f.t.Context(), platformdb.CreateAccountParams{ID: store.NewID(), Name: "Rudolfo"})
	if err != nil {
		f.t.Fatal(err)
	}
	return account.ID
}

func (f *fixture) record(ctx context.Context, account uuid.UUID, key string, req request, failAfterWrite bool) (response, bool, error) {
	var res response
	var replayed bool
	err := f.store.TenantTx(ctx, account, func(q *tenantdb.Queries) error {
		var err error
		res, replayed, err = idempotency.Do(ctx, q, account, "record_expense", key, req, func() (response, error) {
			f.calls.Add(1)
			row, err := q.InsertObservation(ctx, tenantdb.InsertObservationParams{
				ID:         store.NewID(),
				AccountID:  account,
				Source:     "conversation",
				SourceKey:  store.NewID().String(),
				Payload:    []byte(`{}`),
				Redactions: []byte(`[]`),
				ReceivedAt: time.Now(),
			})
			if err != nil {
				return response{}, err
			}
			if failAfterWrite {
				return response{}, errMidway
			}
			return response{ObservationID: row.ID}, nil
		})
		return err
	})
	return res, replayed, err
}

func (f *fixture) observations() int {
	f.t.Helper()
	conn, err := pgx.Connect(f.t.Context(), f.databaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var count int
	if err := conn.QueryRow(f.t.Context(), "SELECT count(*) FROM observations").Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}

func TestARetryAfterATimeoutReplaysTheFirstResponse(t *testing.T) {
	f := newFixture(t)
	req := request{Text: "Gastei R$ 327,39 no mercado", Amount: "327.39"}

	first, replayed, err := f.record(t.Context(), f.account, "request-1", req, false)
	if err != nil || replayed {
		t.Fatalf("first call = %+v, replayed=%v, %v", first, replayed, err)
	}
	second, replayed, err := f.record(t.Context(), f.account, "request-1", req, false)

	if err != nil || !replayed || second != first {
		t.Errorf("retry = %+v, replayed=%v, %v; want the first response replayed", second, replayed, err)
	}
	if f.calls.Load() != 1 || f.observations() != 1 {
		t.Errorf("work ran %d times and wrote %d observations, want once", f.calls.Load(), f.observations())
	}
}

func TestReusingAKeyWithDifferentContentIsAConflict(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.record(t.Context(), f.account, "request-1", request{Amount: "327.39"}, false); err != nil {
		t.Fatal(err)
	}

	_, _, err := f.record(t.Context(), f.account, "request-1", request{Amount: "32.97"}, false)

	if !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
	if f.observations() != 1 {
		t.Errorf("observations = %d, want 1", f.observations())
	}
}

func TestAFailureMidwayLeavesNothingBehindAndTheRetryWorks(t *testing.T) {
	f := newFixture(t)
	req := request{Amount: "327.39"}

	if _, _, err := f.record(t.Context(), f.account, "request-1", req, true); !errors.Is(err, errMidway) {
		t.Fatalf("first call error = %v, want the injected failure", err)
	}
	if f.observations() != 0 {
		t.Fatalf("a failed operation left %d observations behind", f.observations())
	}

	res, replayed, err := f.record(t.Context(), f.account, "request-1", req, false)

	if err != nil || replayed || res.ObservationID == uuid.Nil {
		t.Errorf("retry = %+v, replayed=%v, %v; want a fresh successful run", res, replayed, err)
	}
	if f.observations() != 1 {
		t.Errorf("observations = %d, want 1", f.observations())
	}
}

func TestConcurrentRequestsWithTheSameKeyRunTheWorkOnce(t *testing.T) {
	f := newFixture(t)
	req := request{Amount: "327.39"}
	const attempts = 8

	var wg sync.WaitGroup
	results := make([]response, attempts)
	errs := make([]error, attempts)
	for i := range attempts {
		wg.Go(func() {
			results[i], _, errs[i] = f.record(context.Background(), f.account, "request-1", req, false)
		})
	}
	wg.Wait()

	for i := range attempts {
		if errs[i] != nil || results[i] != results[0] {
			t.Errorf("attempt %d = %+v, %v; want %+v", i, results[i], errs[i], results[0])
		}
	}
	if f.calls.Load() != 1 || f.observations() != 1 {
		t.Errorf("work ran %d times and wrote %d observations, want once", f.calls.Load(), f.observations())
	}
}

func TestKeysAreScopedToTheAccount(t *testing.T) {
	f := newFixture(t)
	req := request{Amount: "327.39"}
	if _, _, err := f.record(t.Context(), f.account, "request-1", req, false); err != nil {
		t.Fatal(err)
	}

	_, replayed, err := f.record(t.Context(), f.newAccount(), "request-1", req, false)

	if err != nil || replayed {
		t.Errorf("another account's call replayed=%v, %v; want an independent run", replayed, err)
	}
}

func TestKeysMustNotBeEmptyOrHuge(t *testing.T) {
	f := newFixture(t)
	for _, key := range []string{"", "   ", string(make([]byte, 201)), "abc\x00def", "line\nbreak"} {
		if _, _, err := f.record(t.Context(), f.account, key, request{}, false); !errors.Is(err, idempotency.ErrInvalidKey) {
			t.Errorf("key of length %d error = %v, want ErrInvalidKey", len(key), err)
		}
	}
}
