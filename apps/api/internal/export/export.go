package export

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
)

const (
	Format       = "lastro-export"
	Version      = 1
	DocumentName = "lastro-export.json"
	LinkLifetime = 15 * time.Minute
)

var (
	ErrUnknownFormat = errors.New("the file is not a Lastro export of a supported version")
	ErrLinkNotFound  = errors.New("the export link does not exist or has expired")
	ErrAccountExists = errors.New("the account of this export already exists in this database")
)

// tables are restored in this order so that every foreign key finds its row.
// Credentials, OAuth state, invites, idempotency keys and audit logs are not
// part of the account's records and are left out.
var tables = []string{
	"accounts",
	"categories",
	"payment_methods",
	"wallet_devices",
	"wallet_tracking_periods",
	"wallet_heartbeats",
	"observations",
	"wallet_notifications",
	"wallet_notification_versions",
	"transactions",
	"observation_interpretations",
	"transaction_evidence",
	"field_provenance",
	"category_allocations",
	"statements",
	"statement_versions",
	"statement_lines",
	"clarifications",
	"revisions",
	"installment_plans",
	"installments",
	"charge_components",
	"transaction_merges",
	"match_rejections",
	"classification_rules",
}

type Document struct {
	Format     string                     `json:"format"`
	Version    int                        `json:"version"`
	ExportedAt time.Time                  `json:"exported_at"`
	AccountID  uuid.UUID                  `json:"account_id"`
	Tables     map[string]json.RawMessage `json:"tables"`
}

type Service struct {
	store *store.Store
}

func NewService(s *store.Store) *Service {
	return &Service{store: s}
}

func (s *Service) Dump(ctx context.Context, accountID uuid.UUID, now time.Time) (Document, error) {
	doc := Document{Format: Format, Version: Version, ExportedAt: now.UTC(), AccountID: accountID, Tables: map[string]json.RawMessage{}}
	err := s.store.TenantSQL(ctx, accountID, func(tx pgx.Tx) error {
		for _, table := range tables {
			owner := "account_id"
			if table == "accounts" {
				owner = "id"
			}
			var rows []byte
			query := fmt.Sprintf("SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM %s t WHERE t.%s = $1", pgx.Identifier{table}.Sanitize(), owner)
			if err := tx.QueryRow(ctx, query, accountID).Scan(&rows); err != nil {
				return fmt.Errorf("export %s: %w", table, err)
			}
			doc.Tables[table] = rows
		}
		return nil
	})
	return doc, err
}

func (s *Service) Restore(ctx context.Context, doc Document) error {
	if doc.Format != Format || doc.Version != Version || doc.AccountID == uuid.Nil {
		return ErrUnknownFormat
	}
	return s.store.TenantSQL(ctx, doc.AccountID, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT FROM accounts WHERE id = $1)", doc.AccountID).Scan(&exists); err != nil {
			return fmt.Errorf("check account: %w", err)
		}
		if exists {
			return ErrAccountExists
		}
		for _, table := range tables {
			rows, ok := doc.Tables[table]
			if !ok {
				continue
			}
			name := pgx.Identifier{table}.Sanitize()
			query := fmt.Sprintf("INSERT INTO %s SELECT * FROM jsonb_populate_recordset(NULL::%s, $1::jsonb)", name, name)
			if _, err := tx.Exec(ctx, query, rows); err != nil {
				return fmt.Errorf("restore %s: %w", table, err)
			}
		}
		return nil
	})
}

// Write packs the complete JSON and the CSV views of an account in one zip.
func (s *Service) Write(ctx context.Context, w io.Writer, doc Document) error {
	sheets, err := s.sheets(ctx, doc.AccountID)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(w)
	file, err := archive.Create(DocumentName)
	if err != nil {
		return fmt.Errorf("add %s: %w", DocumentName, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("write %s: %w", DocumentName, err)
	}
	for _, sheet := range sheets {
		file, err := archive.Create(sheet.name)
		if err != nil {
			return fmt.Errorf("add %s: %w", sheet.name, err)
		}
		writer := csv.NewWriter(file)
		if err := writer.WriteAll(sheet.rows); err != nil {
			return fmt.Errorf("write %s: %w", sheet.name, err)
		}
	}
	return archive.Close()
}

func Read(r io.Reader) (Document, error) {
	var doc Document
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("%w: %w", ErrUnknownFormat, err)
	}
	if doc.Format != Format || doc.Version != Version {
		return Document{}, ErrUnknownFormat
	}
	return doc, nil
}

// NewLink returns a secret that downloads the account's export until it
// expires; only its hash is stored.
func (s *Service) NewLink(ctx context.Context, accountID uuid.UUID, now time.Time) (string, time.Time, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", time.Time{}, fmt.Errorf("generate export token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	expires := now.Add(LinkLifetime)
	err := s.store.Platform().InsertExportLink(ctx, platformdb.InsertExportLinkParams{ID: store.NewID(), AccountID: accountID, TokenHash: hash[:], ExpiresAt: expires})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("record export link: %w", err)
	}
	return token, expires, nil
}

func (s *Service) Resolve(ctx context.Context, token string) (uuid.UUID, error) {
	hash := sha256.Sum256([]byte(token))
	accountID, err := s.store.Platform().FindExportLink(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, ErrLinkNotFound
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("find export link: %w", err)
	}
	return accountID, nil
}
