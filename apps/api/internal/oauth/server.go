package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	AccessTokenPrefix  = "lat_"
	refreshTokenPrefix = "lrt_"
	codePrefix         = "loc_"
	clientIDPrefix     = "lcl_"
	clientSecretPrefix = "lcs_"

	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
	codeTTL         = time.Minute
	metadataTTL     = time.Hour
	secretBytes     = 32

	kindAccess  = "access"
	kindRefresh = "refresh"
)

var (
	ErrUnauthenticated = errors.New("invalid, expired or revoked OAuth token")
	ErrGrantNotFound   = errors.New("OAuth grant not found")

	SupportedScopes = []accounts.Scope{accounts.ScopeExpensesRead, accounts.ScopeExpensesWrite, accounts.ScopeStatementsWrite}
	defaultScopes   = []accounts.Scope{accounts.ScopeExpensesRead, accounts.ScopeExpensesWrite}
)

//go:embed templates/*.html
var templates embed.FS

type Config struct {
	Issuer        *url.URL
	RedirectHosts []string
	MetadataHosts []string
	AllowLoopback bool
	Fetcher       MetadataFetcher
}

type Server struct {
	store    *store.Store
	accounts *accounts.Service
	cfg      Config
	logger   *slog.Logger
	pages    *template.Template
	now      func() time.Time
}

type Grant struct {
	ID         uuid.UUID
	ClientID   string
	ClientName *string
	Scopes     []accounts.Scope
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

func New(s *store.Store, accountsService *accounts.Service, logger *slog.Logger, cfg Config) *Server {
	if cfg.Fetcher == nil {
		cfg.Fetcher = newHTTPFetcher(cfg.AllowLoopback)
	}
	return &Server{
		store:    s,
		accounts: accountsService,
		cfg:      cfg,
		logger:   logger,
		pages:    template.Must(template.ParseFS(templates, "templates/*.html")),
		now:      time.Now,
	}
}

func (s *Server) Issuer() string {
	return s.cfg.Issuer.String()
}

func (s *Server) Resource() string {
	return s.Issuer() + "/mcp"
}

func (s *Server) ResourceMetadataURL() string {
	return s.Issuer() + "/.well-known/oauth-protected-resource"
}

func (s *Server) Authenticate(ctx context.Context, token string) (accounts.Principal, error) {
	id, secret, err := parseSecret(AccessTokenPrefix, token)
	if err != nil {
		return accounts.Principal{}, ErrUnauthenticated
	}
	row, err := s.store.Platform().LookupOAuthToken(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return accounts.Principal{}, fmt.Errorf("look up OAuth token: %w", err)
	}
	valid := row.Kind == kindAccess &&
		secretMatches(secret, row.TokenHash) &&
		row.GrantRevokedAt == nil &&
		s.now().Before(row.ExpiresAt) &&
		row.Resource == s.Resource()
	if !valid {
		return accounts.Principal{}, ErrUnauthenticated
	}
	return accounts.Principal{AccountID: row.AccountID, GrantID: row.GrantID, Scopes: toScopes(row.Scopes)}, nil
}

func (s *Server) Grants(ctx context.Context, accountID uuid.UUID) ([]Grant, error) {
	var grants []Grant
	err := s.store.TenantTx(ctx, accountID, func(q *tenantdb.Queries) error {
		rows, err := q.ListOAuthGrants(ctx, accountID)
		if err != nil {
			return fmt.Errorf("list OAuth grants: %w", err)
		}
		grants = make([]Grant, len(rows))
		for i, row := range rows {
			grants[i] = Grant{
				ID: row.ID, ClientID: row.ClientID, ClientName: row.ClientName, Scopes: toScopes(row.Scopes),
				CreatedAt: row.CreatedAt, LastUsedAt: row.LastUsedAt, RevokedAt: row.RevokedAt,
			}
		}
		return nil
	})
	return grants, err
}

func (s *Server) RevokeGrant(ctx context.Context, accountID, grantID uuid.UUID) (Grant, error) {
	var grant Grant
	err := s.store.TenantTx(ctx, accountID, func(q *tenantdb.Queries) error {
		row, err := q.RevokeAccountOAuthGrant(ctx, tenantdb.RevokeAccountOAuthGrantParams{AccountID: accountID, ID: grantID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGrantNotFound
		}
		if err != nil {
			return fmt.Errorf("revoke OAuth grant: %w", err)
		}
		grant = Grant{ID: row.ID, Scopes: toScopes(row.Scopes), CreatedAt: row.CreatedAt, LastUsedAt: row.LastUsedAt, RevokedAt: row.RevokedAt}
		return nil
	})
	return grant, err
}

type issuedTokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (s *Server) issueTokens(ctx context.Context, q *platformdb.Queries, grantID uuid.UUID, scopes []string) (issuedTokens, error) {
	now := s.now()
	access, accessHash, accessID := newSecret(AccessTokenPrefix)
	refresh, refreshHash, refreshID := newSecret(refreshTokenPrefix)
	for _, token := range []platformdb.CreateOAuthTokenParams{
		{ID: accessID, GrantID: grantID, Kind: kindAccess, TokenHash: accessHash, Scopes: scopes, Resource: s.Resource(), ExpiresAt: now.Add(accessTokenTTL)},
		{ID: refreshID, GrantID: grantID, Kind: kindRefresh, TokenHash: refreshHash, Scopes: scopes, Resource: s.Resource(), ExpiresAt: now.Add(refreshTokenTTL)},
	} {
		if err := q.CreateOAuthToken(ctx, token); err != nil {
			return issuedTokens{}, fmt.Errorf("create %s token: %w", token.Kind, err)
		}
	}
	return issuedTokens{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(accessTokenTTL.Seconds()),
		RefreshToken: refresh,
		Scope:        strings.Join(scopes, " "),
	}, nil
}

func newSecret(prefix string) (token string, hash []byte, id uuid.UUID) {
	id = store.NewID()
	secret := randomBytes(secretBytes)
	return prefix + hex.EncodeToString(id[:]) + "_" + base64.RawURLEncoding.EncodeToString(secret), digest(secret), id
}

func parseSecret(prefix, token string) (uuid.UUID, []byte, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(token), prefix)
	if !ok {
		return uuid.Nil, nil, ErrUnauthenticated
	}
	encodedID, encodedSecret, ok := strings.Cut(rest, "_")
	var id uuid.UUID
	if !ok || len(encodedID) != hex.EncodedLen(len(id)) {
		return uuid.Nil, nil, ErrUnauthenticated
	}
	if _, err := hex.Decode(id[:], []byte(encodedID)); err != nil {
		return uuid.Nil, nil, ErrUnauthenticated
	}
	secret, err := base64.RawURLEncoding.DecodeString(encodedSecret)
	if err != nil || len(secret) != secretBytes {
		return uuid.Nil, nil, ErrUnauthenticated
	}
	return id, secret, nil
}

func opaqueValue(prefix string) (value string, hash []byte) {
	value = prefix + base64.RawURLEncoding.EncodeToString(randomBytes(secretBytes))
	return value, digest([]byte(value))
}

func secretMatches(secret, hash []byte) bool {
	return subtle.ConstantTimeCompare(digest(secret), hash) == 1
}

func digest(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return b
}

func toScopes(values []string) []accounts.Scope {
	scopes := make([]accounts.Scope, len(values))
	for i, value := range values {
		scopes[i] = accounts.Scope(value)
	}
	return scopes
}

func parseScopes(value string) ([]string, bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		fields = make([]string, len(defaultScopes))
		for i, scope := range defaultScopes {
			fields[i] = string(scope)
		}
	}
	var scopes []string
	for _, field := range fields {
		if !slices.Contains(SupportedScopes, accounts.Scope(field)) {
			return nil, false
		}
		if !slices.Contains(scopes, field) {
			scopes = append(scopes, field)
		}
	}
	return scopes, true
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
