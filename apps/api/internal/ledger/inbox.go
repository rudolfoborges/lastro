package ledger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	defaultPendingLimit = 20
	maxPendingLimit     = 50
)

type InterpretationEffect string

const (
	EffectNone          InterpretationEffect = "none"
	EffectCreated       InterpretationEffect = "created"
	EffectLinked        InterpretationEffect = "linked"
	EffectUpdated       InterpretationEffect = "updated"
	EffectVoided        InterpretationEffect = "voided"
	EffectRefundCreated InterpretationEffect = "refund_created"
	EffectUndone        InterpretationEffect = "undone"
)

type NotificationVersion struct {
	ObservationID  uuid.UUID
	NotificationID uuid.UUID
	Version        int
	Event          NotificationEvent
	PostedAt       time.Time
	ReceivedAt     time.Time
	Title          string
	Text           string
	BigText        string
	Interpretation *AppliedInterpretation
}

type AppliedInterpretation struct {
	Version       int
	EventType     EventType
	Effect        InterpretationEffect
	TransactionID *uuid.UUID
}

type PendingNotification struct {
	NotificationVersion
	DeviceID  string
	Package   string
	RemovedAt *time.Time
	Previous  []NotificationVersion
}

type PendingPage struct {
	Notifications []PendingNotification
	NextCursor    string
	Pending       int
	Location      *time.Location
}

type pendingCursor struct {
	receivedAt    time.Time
	observationID uuid.UUID
}

func (s *Service) PendingNotifications(ctx context.Context, accountID uuid.UUID, cursor string, limit int) (PendingPage, error) {
	if limit <= 0 {
		limit = defaultPendingLimit
	}
	if limit > maxPendingLimit {
		return PendingPage{}, &ValidationError{Field: "limit", Message: fmt.Sprintf("must be at most %d", maxPendingLimit)}
	}
	after, err := decodePendingCursor(cursor)
	if err != nil {
		return PendingPage{}, err
	}
	var page PendingPage
	err = s.inTx(ctx, accountID, func(t *tx) error {
		loc, err := t.location()
		if err != nil {
			return err
		}
		page.Location = loc
		params := tenantdb.ListPendingNotificationsParams{AccountID: t.accountID, RowLimit: int32(limit + 1)} //nolint:gosec // bounded above
		if after != nil {
			params.AfterReceivedAt, params.AfterObservationID = &after.receivedAt, &after.observationID
		}
		rows, err := t.q.ListPendingNotifications(t.ctx, params)
		if err != nil {
			return fmt.Errorf("list pending notifications: %w", err)
		}
		if len(rows) > limit {
			rows = rows[:limit]
			last := rows[limit-1]
			page.NextCursor = encodePendingCursor(pendingCursor{receivedAt: last.ReceivedAt, observationID: last.ObservationID})
		}
		pending, err := t.q.CountPendingNotifications(t.ctx, t.accountID)
		if err != nil {
			return fmt.Errorf("count pending notifications: %w", err)
		}
		page.Pending = int(pending)
		page.Notifications, err = t.pendingNotifications(rows)
		return err
	})
	return page, err
}

func (t *tx) pendingNotifications(rows []tenantdb.ListPendingNotificationsRow) ([]PendingNotification, error) {
	var withHistory []uuid.UUID
	for _, row := range rows {
		if row.Version > 1 {
			withHistory = append(withHistory, row.NotificationID)
		}
	}
	history, err := t.notificationHistory(withHistory)
	if err != nil {
		return nil, err
	}
	notifications := make([]PendingNotification, 0, len(rows))
	for _, row := range rows {
		version, err := notificationVersion(row.ObservationID, row.NotificationID, row.Version, row.Event, row.PostedAt, row.ReceivedAt, row.Payload)
		if err != nil {
			return nil, err
		}
		pending := PendingNotification{NotificationVersion: version, DeviceID: row.DeviceID, Package: row.Package, RemovedAt: row.RemovedAt}
		for _, earlier := range history[row.NotificationID] {
			if earlier.Version < version.Version {
				pending.Previous = append(pending.Previous, earlier)
			}
		}
		notifications = append(notifications, pending)
	}
	return notifications, nil
}

func (t *tx) notificationHistory(notificationIDs []uuid.UUID) (map[uuid.UUID][]NotificationVersion, error) {
	history := map[uuid.UUID][]NotificationVersion{}
	if len(notificationIDs) == 0 {
		return history, nil
	}
	rows, err := t.q.ListNotificationVersions(t.ctx, tenantdb.ListNotificationVersionsParams{AccountID: t.accountID, NotificationIds: notificationIDs})
	if err != nil {
		return nil, fmt.Errorf("list notification versions: %w", err)
	}
	observationIDs := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		observationIDs[i] = row.ObservationID
	}
	applied, err := t.appliedInterpretations(observationIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		version, err := notificationVersion(row.ObservationID, row.NotificationID, row.Version, row.Event, row.PostedAt, row.ReceivedAt, row.Payload)
		if err != nil {
			return nil, err
		}
		version.Interpretation = applied[row.ObservationID]
		history[row.NotificationID] = append(history[row.NotificationID], version)
	}
	return history, nil
}

func (t *tx) appliedInterpretations(observationIDs []uuid.UUID) (map[uuid.UUID]*AppliedInterpretation, error) {
	rows, err := t.q.LatestInterpretations(t.ctx, tenantdb.LatestInterpretationsParams{AccountID: t.accountID, ObservationIds: observationIDs})
	if err != nil {
		return nil, fmt.Errorf("list latest interpretations: %w", err)
	}
	applied := make(map[uuid.UUID]*AppliedInterpretation, len(rows))
	for _, row := range rows {
		interpretation := &AppliedInterpretation{Version: int(row.Version), EventType: EventType(row.EventType), Effect: EffectNone, TransactionID: row.TransactionID}
		if row.Effect != nil {
			interpretation.Effect = InterpretationEffect(*row.Effect)
		}
		applied[row.ObservationID] = interpretation
	}
	return applied, nil
}

func notificationVersion(observationID, notificationID uuid.UUID, version int32, event string, postedAt, receivedAt time.Time, payload []byte) (NotificationVersion, error) {
	var content struct {
		Title   string `json:"title"`
		Text    string `json:"text"`
		BigText string `json:"big_text"`
	}
	if err := json.Unmarshal(payload, &content); err != nil {
		return NotificationVersion{}, fmt.Errorf("decode notification payload: %w", err)
	}
	return NotificationVersion{
		ObservationID:  observationID,
		NotificationID: notificationID,
		Version:        int(version),
		Event:          NotificationEvent(event),
		PostedAt:       postedAt,
		ReceivedAt:     receivedAt,
		Title:          content.Title,
		Text:           content.Text,
		BigText:        content.BigText,
	}, nil
}

func (v NotificationVersion) Content() string {
	parts := []string{}
	for _, part := range []string{v.Title, v.Text} {
		if strings.TrimSpace(part) != "" {
			parts = append(parts, strings.TrimSpace(part))
		}
	}
	if big := strings.TrimSpace(v.BigText); big != "" && big != strings.TrimSpace(v.Text) {
		parts = append(parts, big)
	}
	return strings.Join(parts, " — ")
}

func encodePendingCursor(c pendingCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.receivedAt.UTC().Format(time.RFC3339Nano) + "|" + c.observationID.String()))
}

func decodePendingCursor(cursor string) (*pendingCursor, error) {
	if strings.TrimSpace(cursor) == "" {
		return nil, nil
	}
	invalid := &ValidationError{Field: "cursor", Message: "must be a next_cursor returned by list_pending_notifications"}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return nil, invalid
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil, invalid
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, invalid
	}
	observationID, err := uuid.Parse(id)
	if err != nil {
		return nil, invalid
	}
	return &pendingCursor{receivedAt: receivedAt, observationID: observationID}, nil
}
