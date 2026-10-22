package accounts_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

func newService(t *testing.T) *accounts.Service {
	t.Helper()
	s, err := store.Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return accounts.NewService(s)
}

func signUp(t *testing.T, svc *accounts.Service, name string) (accounts.Account, accounts.IssuedKey) {
	t.Helper()
	invite, err := svc.CreateInvite(t.Context(), time.Hour, "")
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}
	account, key, err := svc.SignUp(t.Context(), invite.Code, name)
	if err != nil {
		t.Fatalf("SignUp() error = %v", err)
	}
	return account, key
}

func createKey(t *testing.T, svc *accounts.Service, account accounts.Account, name string, scopes ...string) accounts.IssuedKey {
	t.Helper()
	key, err := svc.CreateKey(t.Context(), account.ID, name, scopes)
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	return key
}

func assertValidationError(t *testing.T, err error, field string) {
	t.Helper()
	var validation *accounts.ValidationError
	if !errors.As(err, &validation) || validation.Field != field {
		t.Errorf("error = %v, want validation error on %s", err, field)
	}
}

func TestSignUpCreatesAnAccountWithAnAdminKey(t *testing.T) {
	svc := newService(t)

	account, key := signUp(t, svc, "  Rudolfo  ")

	if account.Name != "Rudolfo" {
		t.Errorf("account name = %q, want trimmed name", account.Name)
	}
	if key.Name != "admin" || len(key.Scopes) != 1 || key.Scopes[0] != accounts.ScopeAdmin {
		t.Errorf("issued key = %+v, want a single admin scope", key.APIKey)
	}
	principal, err := svc.Authenticate(t.Context(), key.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if principal.AccountID != account.ID || principal.KeyID != key.ID || !principal.Has(accounts.ScopeAdmin) {
		t.Errorf("principal = %+v", principal)
	}
}

func TestAnInviteCanBeUsedOnlyOnce(t *testing.T) {
	svc := newService(t)
	invite, err := svc.CreateInvite(t.Context(), time.Hour, "for Rudolfo")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.SignUp(t.Context(), invite.Code, "First"); err != nil {
		t.Fatalf("first SignUp() error = %v", err)
	}

	_, _, err = svc.SignUp(t.Context(), invite.Code, "Second")

	if !errors.Is(err, accounts.ErrInvalidInvite) {
		t.Errorf("second SignUp() error = %v, want ErrInvalidInvite", err)
	}
}

func TestExpiredAndUnknownInvitesAreRejected(t *testing.T) {
	svc := newService(t)
	expired, err := svc.CreateInvite(t.Context(), time.Nanosecond, "")
	if err != nil {
		t.Fatal(err)
	}

	for name, code := range map[string]string{"expired": expired.Code, "unknown": "inv_does-not-exist"} {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.SignUp(t.Context(), code, "Someone")
			if !errors.Is(err, accounts.ErrInvalidInvite) {
				t.Errorf("SignUp() error = %v, want ErrInvalidInvite", err)
			}
		})
	}
}

func TestSignUpValidatesInput(t *testing.T) {
	svc := newService(t)

	_, _, err := svc.SignUp(t.Context(), "inv_x", " ")
	assertValidationError(t, err, "name")

	_, _, err = svc.SignUp(t.Context(), "", "Rudolfo")
	assertValidationError(t, err, "invite_code")
}

func TestAuthenticateRejectsWrongSecretsAndMalformedTokens(t *testing.T) {
	svc := newService(t)
	_, key := signUp(t, svc, "Rudolfo")
	tampered := key.Token[:len(key.Token)-2] + "AA"
	if tampered == key.Token {
		tampered = key.Token[:len(key.Token)-2] + "BB"
	}

	for name, token := range map[string]string{"tampered": tampered, "malformed": "Bearer nope"} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Authenticate(t.Context(), token); !errors.Is(err, accounts.ErrUnauthenticated) {
				t.Errorf("Authenticate() error = %v, want ErrUnauthenticated", err)
			}
		})
	}
}

func TestRevokedKeyStopsWorkingImmediately(t *testing.T) {
	svc := newService(t)
	account, _ := signUp(t, svc, "Rudolfo")
	android := createKey(t, svc, account, "android", "wallet:ingest")

	if _, err := svc.RevokeKey(t.Context(), account.ID, android.ID); err != nil {
		t.Fatalf("RevokeKey() error = %v", err)
	}

	if _, err := svc.Authenticate(t.Context(), android.Token); !errors.Is(err, accounts.ErrUnauthenticated) {
		t.Errorf("Authenticate() after revoke error = %v, want ErrUnauthenticated", err)
	}
}

func TestCreateKeyValidatesAndDeduplicatesScopes(t *testing.T) {
	svc := newService(t)
	account, _ := signUp(t, svc, "Rudolfo")

	key := createKey(t, svc, account, "case", "expenses:read", "expenses:write", "expenses:read")
	if len(key.Scopes) != 2 {
		t.Errorf("scopes = %v, want two distinct scopes", key.Scopes)
	}

	_, err := svc.CreateKey(t.Context(), account.ID, "case", []string{"expenses:delete"})
	assertValidationError(t, err, "scopes")

	_, err = svc.CreateKey(t.Context(), account.ID, "case", nil)
	assertValidationError(t, err, "scopes")
}

func TestAccountsOnlySeeAndRevokeTheirOwnKeys(t *testing.T) {
	svc := newService(t)
	alice, aliceAdmin := signUp(t, svc, "Alice")
	_, bobAdmin := signUp(t, svc, "Bob")

	keys, err := svc.ListKeys(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].ID != aliceAdmin.ID {
		t.Errorf("Alice's keys = %+v, want only her admin key", keys)
	}

	_, err = svc.RevokeKey(t.Context(), alice.ID, bobAdmin.ID)
	if !errors.Is(err, accounts.ErrKeyNotFound) {
		t.Errorf("RevokeKey(Bob's key) as Alice error = %v, want ErrKeyNotFound", err)
	}
	if _, err := svc.Authenticate(t.Context(), bobAdmin.Token); err != nil {
		t.Errorf("Bob's key stopped working: %v", err)
	}
}

func TestTheLastActiveAdminKeyCannotBeRevoked(t *testing.T) {
	svc := newService(t)
	account, admin := signUp(t, svc, "Rudolfo")

	_, err := svc.RevokeKey(t.Context(), account.ID, admin.ID)
	if !errors.Is(err, accounts.ErrLastAdminKey) {
		t.Fatalf("RevokeKey(last admin) error = %v, want ErrLastAdminKey", err)
	}

	backup := createKey(t, svc, account, "backup admin", "admin")
	revoked, err := svc.RevokeKey(t.Context(), account.ID, admin.ID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("RevokeKey(admin with backup) = %+v, %v", revoked, err)
	}
	again, err := svc.RevokeKey(t.Context(), account.ID, admin.ID)
	if err != nil || !again.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Errorf("revoking twice = %+v, %v; want the same revocation", again, err)
	}
	if _, err := svc.Authenticate(t.Context(), backup.Token); err != nil {
		t.Errorf("backup admin key stopped working: %v", err)
	}
}

func TestRecordRequestMarksTheKeyAsUsed(t *testing.T) {
	svc := newService(t)
	account, admin := signUp(t, svc, "Rudolfo")
	principal, err := svc.Authenticate(t.Context(), admin.Token)
	if err != nil {
		t.Fatal(err)
	}

	err = svc.RecordRequest(t.Context(), principal, accounts.RequestRecord{
		RequestID: "req-1", Method: "GET", Route: "GET /v1/whoami", Status: 200, Duration: 3 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RecordRequest() error = %v", err)
	}

	keys, err := svc.ListKeys(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if keys[0].LastUsedAt == nil {
		t.Error("LastUsedAt is nil after a recorded request")
	}
}

func TestWhoamiDescribesTheCallingKey(t *testing.T) {
	svc := newService(t)
	account, _ := signUp(t, svc, "Rudolfo")
	caseKey := createKey(t, svc, account, "case", "expenses:write")
	principal, err := svc.Authenticate(t.Context(), caseKey.Token)
	if err != nil {
		t.Fatal(err)
	}

	gotAccount, gotKey, err := svc.Whoami(t.Context(), principal)

	if err != nil {
		t.Fatalf("Whoami() error = %v", err)
	}
	if gotAccount.Name != "Rudolfo" || gotKey.Name != "case" {
		t.Errorf("Whoami() = %+v, %+v", gotAccount, gotKey)
	}
}

func TestTokensAreStoredOnlyAsHashes(t *testing.T) {
	db, err := store.Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	svc := accounts.NewService(db)
	_, key := signUp(t, svc, "Rudolfo")

	stored, err := db.Platform().GetAPIKeyCredentials(t.Context(), key.ID)

	if err != nil {
		t.Fatal(err)
	}
	if len(stored.TokenHash) != 32 || bytes.Contains([]byte(key.Token), stored.TokenHash) || bytes.Contains(stored.TokenHash, []byte(key.Token)) {
		t.Errorf("stored token hash has %d bytes; want a 32-byte digest unrelated to the token text", len(stored.TokenHash))
	}
}
