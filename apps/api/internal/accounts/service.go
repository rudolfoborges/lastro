package accounts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/taxonomy"
)

const (
	adminKeyName  = "admin"
	maxNameLength = 100
)

var (
	ErrInvalidInvite   = errors.New("invite is invalid, expired or already used")
	ErrUnauthenticated = errors.New("invalid or revoked API key")
	ErrKeyNotFound     = errors.New("API key not found")
	ErrLastAdminKey    = errors.New("the last active admin key cannot be revoked")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + " " + e.Message
}

type Account struct {
	ID        uuid.UUID
	Name      string
	Timezone  string
	CreatedAt time.Time
}

type APIKey struct {
	ID         uuid.UUID
	AccountID  uuid.UUID
	Name       string
	Scopes     []Scope
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type IssuedKey struct {
	APIKey
	Token string
}

type Invite struct {
	ID        uuid.UUID
	Code      string
	Note      *string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Principal struct {
	AccountID uuid.UUID
	KeyID     uuid.UUID
	Scopes    []Scope
}

func (p Principal) Has(scope Scope) bool {
	return slices.Contains(p.Scopes, scope)
}

type CredentialKind string

const CredentialAPIKey CredentialKind = "api_key"

func (p Principal) CredentialKind() CredentialKind {
	return CredentialAPIKey
}

func (p Principal) CredentialID() uuid.UUID {
	return p.KeyID
}

type ToolCallRecord struct {
	Tool      string
	Outcome   string
	ErrorCode string
	Duration  time.Duration
}

type RequestRecord struct {
	RequestID string
	Method    string
	Route     string
	Status    int
	Duration  time.Duration
}

type Service struct {
	store *store.Store
}

func NewService(s *store.Store) *Service {
	return &Service{store: s}
}

func (s *Service) CreateInvite(ctx context.Context, ttl time.Duration, note string) (Invite, error) {
	if ttl <= 0 {
		return Invite{}, &ValidationError{Field: "expires_in", Message: "must be positive"}
	}
	code, hash := issueInviteCode()
	row, err := s.store.Platform().CreateInvite(ctx, platformdb.CreateInviteParams{
		ID:        store.NewID(),
		CodeHash:  hash,
		Note:      optional(strings.TrimSpace(note)),
		ExpiresAt: time.Now().Add(ttl),
	})
	if err != nil {
		return Invite{}, fmt.Errorf("create invite: %w", err)
	}
	return Invite{ID: row.ID, Code: code, Note: row.Note, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}, nil
}

func (s *Service) SignUp(ctx context.Context, inviteCode, accountName string) (Account, IssuedKey, error) {
	name, err := validName("name", accountName)
	if err != nil {
		return Account{}, IssuedKey{}, err
	}
	if strings.TrimSpace(inviteCode) == "" {
		return Account{}, IssuedKey{}, &ValidationError{Field: "invite_code", Message: "is required"}
	}

	var account Account
	var issued IssuedKey
	err = s.store.PlatformTx(ctx, func(q *platformdb.Queries) error {
		row, err := q.CreateAccount(ctx, platformdb.CreateAccountParams{ID: store.NewID(), Name: name})
		if err != nil {
			return fmt.Errorf("create account: %w", err)
		}
		account = fromPlatformAccount(row)
		for _, category := range taxonomy.Defaults {
			err := q.CreateCategory(ctx, platformdb.CreateCategoryParams{ID: store.NewID(), AccountID: account.ID, Code: category.Code, Label: category.Label})
			if err != nil {
				return fmt.Errorf("create default category %s: %w", category.Code, err)
			}
		}

		_, err = q.RedeemInvite(ctx, platformdb.RedeemInviteParams{CodeHash: inviteCodeHash(inviteCode), AccountID: &account.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidInvite
		}
		if err != nil {
			return fmt.Errorf("redeem invite: %w", err)
		}

		keyID := store.NewID()
		token, hash := issueToken(keyID)
		key, err := q.CreateAPIKey(ctx, platformdb.CreateAPIKeyParams{
			ID:        keyID,
			AccountID: account.ID,
			Name:      adminKeyName,
			TokenHash: hash,
			Scopes:    []string{string(ScopeAdmin)},
		})
		if err != nil {
			return fmt.Errorf("create admin key: %w", err)
		}
		issued = IssuedKey{APIKey: fromPlatformKey(key), Token: token}
		return nil
	})
	if err != nil {
		return Account{}, IssuedKey{}, err
	}
	return account, issued, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	keyID, secret, err := parseToken(token)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	row, err := s.store.Platform().GetAPIKeyCredentials(ctx, keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("look up API key: %w", err)
	}
	if !secretMatches(secret, row.TokenHash) || row.RevokedAt != nil {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{AccountID: row.AccountID, KeyID: row.ID, Scopes: toScopes(row.Scopes)}, nil
}

func (s *Service) Whoami(ctx context.Context, p Principal) (Account, APIKey, error) {
	var account Account
	var key APIKey
	err := s.store.TenantTx(ctx, p.AccountID, func(q *tenantdb.Queries) error {
		accountRow, err := q.GetAccount(ctx, p.AccountID)
		if err != nil {
			return fmt.Errorf("get account: %w", err)
		}
		keyRow, err := q.GetAPIKey(ctx, tenantdb.GetAPIKeyParams{AccountID: p.AccountID, ID: p.KeyID})
		if err != nil {
			return fmt.Errorf("get API key: %w", err)
		}
		account = fromTenantAccount(accountRow)
		key = fromTenantKey(keyRow)
		return nil
	})
	return account, key, err
}

func (s *Service) Location(ctx context.Context, accountID uuid.UUID) (*time.Location, error) {
	var name string
	err := s.store.TenantTx(ctx, accountID, func(q *tenantdb.Queries) error {
		row, err := q.GetAccount(ctx, accountID)
		if err != nil {
			return fmt.Errorf("get account: %w", err)
		}
		name = row.Timezone
		return nil
	})
	if err != nil {
		return nil, err
	}
	return calendar.LoadTimeZone(name)
}

func (s *Service) SetTimezone(ctx context.Context, accountID uuid.UUID, timezone string) (Account, error) {
	name := strings.TrimSpace(timezone)
	if _, err := calendar.LoadTimeZone(name); err != nil {
		return Account{}, &ValidationError{Field: "timezone", Message: "must be an IANA time zone such as " + calendar.DefaultTimeZone}
	}
	var account Account
	err := s.store.TenantTx(ctx, accountID, func(q *tenantdb.Queries) error {
		row, err := q.UpdateAccountTimezone(ctx, tenantdb.UpdateAccountTimezoneParams{ID: accountID, Timezone: name})
		if err != nil {
			return fmt.Errorf("update account time zone: %w", err)
		}
		account = fromTenantAccount(row)
		return nil
	})
	return account, err
}

func (s *Service) ListKeys(ctx context.Context, accountID uuid.UUID) ([]APIKey, error) {
	var keys []APIKey
	err := s.store.TenantTx(ctx, accountID, func(q *tenantdb.Queries) error {
		rows, err := q.ListAPIKeys(ctx, accountID)
		if err != nil {
			return fmt.Errorf("list API keys: %w", err)
		}
		keys = make([]APIKey, 0, len(rows))
		for _, row := range rows {
			keys = append(keys, fromTenantKey(row))
		}
		return nil
	})
	return keys, err
}

func (s *Service) CreateKey(ctx context.Context, accountID uuid.UUID, name string, scopes []string) (IssuedKey, error) {
	name, err := validName("name", name)
	if err != nil {
		return IssuedKey{}, err
	}
	validScopes, err := validateScopes(scopes)
	if err != nil {
		return IssuedKey{}, err
	}

	var issued IssuedKey
	err = s.store.TenantTx(ctx, accountID, func(q *tenantdb.Queries) error {
		keyID := store.NewID()
		token, hash := issueToken(keyID)
		row, err := q.CreateAPIKey(ctx, tenantdb.CreateAPIKeyParams{
			ID:        keyID,
			AccountID: accountID,
			Name:      name,
			TokenHash: hash,
			Scopes:    validScopes,
		})
		if err != nil {
			return fmt.Errorf("create API key: %w", err)
		}
		issued = IssuedKey{APIKey: fromTenantKey(row), Token: token}
		return nil
	})
	return issued, err
}

func (s *Service) RevokeKey(ctx context.Context, accountID, keyID uuid.UUID) (APIKey, error) {
	var revoked APIKey
	err := s.store.TenantTx(ctx, accountID, func(q *tenantdb.Queries) error {
		if _, err := q.LockAccount(ctx, accountID); err != nil {
			return fmt.Errorf("lock account: %w", err)
		}
		key, err := q.GetAPIKey(ctx, tenantdb.GetAPIKeyParams{AccountID: accountID, ID: keyID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("get API key: %w", err)
		}
		if key.RevokedAt != nil {
			revoked = fromTenantKey(key)
			return nil
		}
		if slices.Contains(key.Scopes, string(ScopeAdmin)) {
			others, err := q.CountOtherActiveAdminKeys(ctx, tenantdb.CountOtherActiveAdminKeysParams{AccountID: accountID, ID: keyID})
			if err != nil {
				return fmt.Errorf("count admin keys: %w", err)
			}
			if others == 0 {
				return ErrLastAdminKey
			}
		}
		row, err := q.RevokeAPIKey(ctx, tenantdb.RevokeAPIKeyParams{AccountID: accountID, ID: keyID})
		if err != nil {
			return fmt.Errorf("revoke API key: %w", err)
		}
		revoked = fromTenantKey(row)
		return nil
	})
	return revoked, err
}

func (s *Service) RecordRequest(ctx context.Context, p Principal, record RequestRecord) error {
	return s.store.TenantTx(ctx, p.AccountID, func(q *tenantdb.Queries) error {
		err := q.RecordAPIRequest(ctx, tenantdb.RecordAPIRequestParams{
			ID:         store.NewID(),
			AccountID:  p.AccountID,
			ApiKeyID:   p.KeyID,
			RequestID:  record.RequestID,
			Method:     record.Method,
			Route:      record.Route,
			Status:     clampInt32(int64(record.Status)),
			DurationMs: clampInt32(record.Duration.Milliseconds()),
		})
		if err != nil {
			return fmt.Errorf("record API request: %w", err)
		}
		return q.TouchAPIKey(ctx, tenantdb.TouchAPIKeyParams{AccountID: p.AccountID, ID: p.KeyID})
	})
}

func (s *Service) RecordToolCall(ctx context.Context, p Principal, record ToolCallRecord) error {
	return s.store.TenantTx(ctx, p.AccountID, func(q *tenantdb.Queries) error {
		params := tenantdb.RecordToolCallParams{
			ID:             store.NewID(),
			AccountID:      p.AccountID,
			CredentialKind: string(p.CredentialKind()),
			CredentialID:   p.CredentialID(),
			Tool:           record.Tool,
			Outcome:        record.Outcome,
			DurationMs:     clampInt32(record.Duration.Milliseconds()),
		}
		if record.ErrorCode != "" {
			params.ErrorCode = &record.ErrorCode
		}
		if err := q.RecordToolCall(ctx, params); err != nil {
			return fmt.Errorf("record tool call: %w", err)
		}
		return nil
	})
}

func validName(field, value string) (string, error) {
	name := strings.TrimSpace(value)
	switch {
	case name == "":
		return "", &ValidationError{Field: field, Message: "is required"}
	case len([]rune(name)) > maxNameLength:
		return "", &ValidationError{Field: field, Message: fmt.Sprintf("must have at most %d characters", maxNameLength)}
	}
	return name, nil
}

func validateScopes(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, &ValidationError{Field: "scopes", Message: "must list at least one scope"}
	}
	scopes := make([]string, 0, len(values))
	for _, value := range values {
		if !Scope(value).valid() {
			return nil, &ValidationError{Field: "scopes", Message: fmt.Sprintf("contains unknown scope %q", value)}
		}
		if !slices.Contains(scopes, value) {
			scopes = append(scopes, value)
		}
	}
	return scopes, nil
}

func toScopes(values []string) []Scope {
	scopes := make([]Scope, len(values))
	for i, value := range values {
		scopes[i] = Scope(value)
	}
	return scopes
}

func fromTenantAccount(row tenantdb.Account) Account {
	return Account{ID: row.ID, Name: row.Name, Timezone: row.Timezone, CreatedAt: row.CreatedAt}
}

func fromPlatformAccount(row platformdb.Account) Account {
	return Account{ID: row.ID, Name: row.Name, Timezone: row.Timezone, CreatedAt: row.CreatedAt}
}

func fromTenantKey(row tenantdb.ApiKey) APIKey {
	return APIKey{
		ID:         row.ID,
		AccountID:  row.AccountID,
		Name:       row.Name,
		Scopes:     toScopes(row.Scopes),
		CreatedAt:  row.CreatedAt,
		LastUsedAt: row.LastUsedAt,
		RevokedAt:  row.RevokedAt,
	}
}

func fromPlatformKey(row platformdb.ApiKey) APIKey {
	return APIKey{
		ID:         row.ID,
		AccountID:  row.AccountID,
		Name:       row.Name,
		Scopes:     toScopes(row.Scopes),
		CreatedAt:  row.CreatedAt,
		LastUsedAt: row.LastUsedAt,
		RevokedAt:  row.RevokedAt,
	}
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func clampInt32(value int64) int32 {
	const maxInt32 = 1<<31 - 1
	if value > maxInt32 {
		return maxInt32
	}
	if value < 0 {
		return 0
	}
	return int32(value)
}
