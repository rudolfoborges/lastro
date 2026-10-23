package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

type NotificationEvent string

const (
	NotificationPosted  NotificationEvent = "posted"
	NotificationUpdated NotificationEvent = "updated"
	NotificationRemoved NotificationEvent = "removed"
)

var notificationEvents = []NotificationEvent{NotificationPosted, NotificationUpdated, NotificationRemoved}

type IngestResult string

const (
	IngestStored              IngestResult = "stored"
	IngestDuplicate           IngestResult = "duplicate"
	IngestDiscarded           IngestResult = "discarded"
	IngestRemoved             IngestResult = "removed"
	IngestUnknownNotification IngestResult = "unknown_notification"
)

const (
	maxNotificationKey  = 300
	maxNotificationText = 5000
	maxFuturePostedAt   = 5 * time.Minute
)

var (
	devicePattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
	packagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
)

type WalletNotification struct {
	DeviceID        string
	Package         string
	NotificationKey string
	Event           NotificationEvent
	PostedAt        time.Time
	Title           string
	Text            string
	BigText         string
	ReceivedAt      time.Time
}

type IngestReceipt struct {
	Result         IngestResult
	NotificationID *uuid.UUID
	Version        int
	ObservationID  *uuid.UUID
}

func (s *Service) IngestNotification(ctx context.Context, accountID uuid.UUID, in WalletNotification) (IngestReceipt, error) {
	if in.ReceivedAt.IsZero() {
		in.ReceivedAt = time.Now()
	}
	if err := validateNotification(in); err != nil {
		return IngestReceipt{}, err
	}
	var receipt IngestReceipt
	err := s.inTx(ctx, accountID, func(t *tx) error {
		device, err := t.q.UpsertWalletDevice(t.ctx, tenantdb.UpsertWalletDeviceParams{
			ID:         store.NewID(),
			AccountID:  t.accountID,
			DeviceID:   in.DeviceID,
			ReceivedAt: in.ReceivedAt.UTC(),
		})
		if err != nil {
			return fmt.Errorf("upsert wallet device: %w", err)
		}
		switch {
		case !redaction.AllowedNotificationPackage(in.Package):
			receipt, err = t.discardNotification(device.ID, in)
		case in.Event == NotificationRemoved:
			receipt, err = t.recordRemoval(device.ID, in)
		default:
			receipt, err = t.recordNotificationVersion(device.ID, in)
		}
		return err
	})
	return receipt, err
}

func (t *tx) discardNotification(deviceID uuid.UUID, in WalletNotification) (IngestReceipt, error) {
	if in.Event != NotificationRemoved {
		if err := t.q.CountDiscardedNotification(t.ctx, tenantdb.CountDiscardedNotificationParams{AccountID: t.accountID, ID: deviceID}); err != nil {
			return IngestReceipt{}, fmt.Errorf("count discarded notification: %w", err)
		}
	}
	return IngestReceipt{Result: IngestDiscarded}, nil
}

func (t *tx) recordRemoval(deviceID uuid.UUID, in WalletNotification) (IngestReceipt, error) {
	notification, err := t.q.LockWalletNotificationByKey(t.ctx, tenantdb.LockWalletNotificationByKeyParams{
		AccountID:       t.accountID,
		WalletDeviceID:  deviceID,
		NotificationKey: in.NotificationKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return IngestReceipt{Result: IngestUnknownNotification}, nil
	}
	if err != nil {
		return IngestReceipt{}, fmt.Errorf("find wallet notification: %w", err)
	}
	removedAt := in.ReceivedAt.UTC()
	if _, err := t.q.MarkWalletNotificationRemoved(t.ctx, tenantdb.MarkWalletNotificationRemovedParams{
		AccountID: t.accountID,
		ID:        notification.ID,
		RemovedAt: &removedAt,
	}); err != nil {
		return IngestReceipt{}, fmt.Errorf("mark wallet notification removed: %w", err)
	}
	return IngestReceipt{Result: IngestRemoved, NotificationID: &notification.ID}, nil
}

func (t *tx) recordNotificationVersion(deviceID uuid.UUID, in WalletNotification) (IngestReceipt, error) {
	notification, err := t.q.UpsertWalletNotification(t.ctx, tenantdb.UpsertWalletNotificationParams{
		ID:              store.NewID(),
		AccountID:       t.accountID,
		WalletDeviceID:  deviceID,
		NotificationKey: in.NotificationKey,
		Package:         in.Package,
	})
	if err != nil {
		return IngestReceipt{}, fmt.Errorf("upsert wallet notification: %w", err)
	}
	content, findings := redaction.Values(map[string]any{"title": in.Title, "text": in.Text, "big_text": in.BigText})
	hash := notificationContentHash(in.Package, content, in.PostedAt)
	existing, err := t.q.FindWalletNotificationVersion(t.ctx, tenantdb.FindWalletNotificationVersionParams{
		AccountID:      t.accountID,
		NotificationID: notification.ID,
		ContentHash:    hash,
	})
	switch {
	case err == nil:
		return IngestReceipt{Result: IngestDuplicate, NotificationID: &notification.ID, Version: int(existing.Version), ObservationID: &existing.ObservationID}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return IngestReceipt{}, fmt.Errorf("find wallet notification version: %w", err)
	}

	observation, err := t.recordObservation(NewObservation{
		Source:    SourceWalletNotification,
		SourceKey: notification.ID.String() + "/" + hex.EncodeToString(hash),
		Payload: map[string]any{
			"device_id":        in.DeviceID,
			"package":          in.Package,
			"notification_key": in.NotificationKey,
			"event":            string(in.Event),
			"posted_at":        in.PostedAt.UTC().Format(time.RFC3339Nano),
			"title":            content["title"],
			"text":             content["text"],
			"big_text":         content["big_text"],
		},
		Redactions: findings,
		ReceivedAt: in.ReceivedAt,
	})
	if err != nil {
		return IngestReceipt{}, err
	}
	version, err := t.q.InsertWalletNotificationVersion(t.ctx, tenantdb.InsertWalletNotificationVersionParams{
		AccountID:      t.accountID,
		NotificationID: notification.ID,
		ObservationID:  observation.ID,
		ContentHash:    hash,
		Event:          string(in.Event),
		PostedAt:       in.PostedAt.UTC(),
		ReceivedAt:     in.ReceivedAt.UTC(),
	})
	if err != nil {
		return IngestReceipt{}, fmt.Errorf("insert wallet notification version: %w", err)
	}
	return IngestReceipt{Result: IngestStored, NotificationID: &notification.ID, Version: int(version.Version), ObservationID: &observation.ID}, nil
}

// posted_at is part of the content identity: a replay of the same post keeps
// it, while a second identical purchase posted under a reused key does not.
func notificationContentHash(pkg string, content map[string]any, postedAt time.Time) []byte {
	parts := []string{pkg}
	for _, field := range []string{"title", "text", "big_text"} {
		value, _ := content[field].(string)
		parts = append(parts, value)
	}
	parts = append(parts, postedAt.UTC().Format(time.RFC3339Nano))
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return sum[:]
}

func validateNotification(in WalletNotification) error {
	if !devicePattern.MatchString(in.DeviceID) {
		return &ValidationError{Field: "device_id", Message: "must have 1 to 100 letters, digits, '.', '_', ':' or '-'"}
	}
	if len(in.Package) > 255 || !packagePattern.MatchString(in.Package) {
		return &ValidationError{Field: "package", Message: "must be an Android package name, such as com.google.android.apps.walletnfcrel"}
	}
	if in.NotificationKey == "" || len([]rune(in.NotificationKey)) > maxNotificationKey {
		return &ValidationError{Field: "notification_key", Message: fmt.Sprintf("must have 1 to %d characters", maxNotificationKey)}
	}
	if !slices.Contains(notificationEvents, in.Event) {
		return &ValidationError{Field: "event", Message: fmt.Sprintf("must be one of %v", notificationEvents)}
	}
	if in.Event == NotificationRemoved {
		return nil
	}
	if in.PostedAt.IsZero() {
		return &ValidationError{Field: "posted_at", Message: "is required for posted and updated notifications"}
	}
	if in.PostedAt.After(in.ReceivedAt.Add(maxFuturePostedAt)) {
		return &ValidationError{Field: "posted_at", Message: "is in the future; check the device clock"}
	}
	empty := true
	for _, field := range []struct{ name, value string }{{"title", in.Title}, {"text", in.Text}, {"big_text", in.BigText}} {
		if len([]rune(field.value)) > maxNotificationText {
			return &ValidationError{Field: field.name, Message: fmt.Sprintf("must have at most %d characters", maxNotificationText)}
		}
		if strings.TrimSpace(field.value) != "" {
			empty = false
		}
	}
	if empty {
		return &ValidationError{Field: "text", Message: "title, text or big_text must have content"}
	}
	return nil
}
