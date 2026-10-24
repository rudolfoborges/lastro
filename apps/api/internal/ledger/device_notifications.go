package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	defaultDeviceNotificationLimit = 50
	maxDeviceNotificationLimit     = 100
)

type DeviceNotification struct {
	NotificationID  uuid.UUID
	NotificationKey string
	Version         int
	Event           NotificationEvent
	PostedAt        time.Time
	ReceivedAt      time.Time
	RemovedAt       *time.Time
	Interpretation  *AppliedInterpretation
	Transaction     *TransactionState
}

type TransactionState struct {
	Status   Status
	Category *CategoryState
}

type CategoryState struct {
	Code   string
	Label  string
	Status AllocationStatus
}

func (s *Service) DeviceNotifications(ctx context.Context, accountID uuid.UUID, deviceID string, limit int) ([]DeviceNotification, error) {
	if !devicePattern.MatchString(deviceID) {
		return nil, &ValidationError{Field: "device_id", Message: "must have 1 to 100 letters, digits, '.', '_', ':' or '-'"}
	}
	if limit <= 0 {
		limit = defaultDeviceNotificationLimit
	}
	if limit > maxDeviceNotificationLimit {
		return nil, &ValidationError{Field: "limit", Message: fmt.Sprintf("must be at most %d", maxDeviceNotificationLimit)}
	}
	var notifications []DeviceNotification
	err := s.inTx(ctx, accountID, func(t *tx) error {
		rows, err := t.q.ListDeviceNotificationVersions(t.ctx, tenantdb.ListDeviceNotificationVersionsParams{
			AccountID: t.accountID,
			DeviceID:  deviceID,
			RowLimit:  int32(limit), //nolint:gosec // bounded above
		})
		if err != nil {
			return fmt.Errorf("list device notification versions: %w", err)
		}
		notifications, err = t.deviceNotifications(rows)
		return err
	})
	return notifications, err
}

func (t *tx) deviceNotifications(rows []tenantdb.ListDeviceNotificationVersionsRow) ([]DeviceNotification, error) {
	notifications := make([]DeviceNotification, 0, len(rows))
	if len(rows) == 0 {
		return notifications, nil
	}
	observationIDs := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		observationIDs[i] = row.ObservationID
	}
	applied, err := t.appliedInterpretations(observationIDs)
	if err != nil {
		return nil, err
	}
	var transactionIDs []uuid.UUID
	for _, interpretation := range applied {
		if interpretation.TransactionID != nil {
			transactionIDs = append(transactionIDs, *interpretation.TransactionID)
		}
	}
	states, err := t.transactionStates(transactionIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		notification := DeviceNotification{
			NotificationID:  row.NotificationID,
			NotificationKey: row.NotificationKey,
			Version:         int(row.Version),
			Event:           NotificationEvent(row.Event),
			PostedAt:        row.PostedAt,
			ReceivedAt:      row.ReceivedAt,
			RemovedAt:       row.RemovedAt,
			Interpretation:  applied[row.ObservationID],
		}
		if notification.Interpretation != nil && notification.Interpretation.TransactionID != nil {
			notification.Transaction = states[*notification.Interpretation.TransactionID]
		}
		notifications = append(notifications, notification)
	}
	return notifications, nil
}

func (t *tx) transactionStates(ids []uuid.UUID) (map[uuid.UUID]*TransactionState, error) {
	states := map[uuid.UUID]*TransactionState{}
	if len(ids) == 0 {
		return states, nil
	}
	rows, err := t.q.TransactionStatuses(t.ctx, tenantdb.TransactionStatusesParams{AccountID: t.accountID, TransactionIds: ids})
	if err != nil {
		return nil, fmt.Errorf("list transaction statuses: %w", err)
	}
	for _, row := range rows {
		states[row.ID] = &TransactionState{Status: Status(row.Status)}
	}
	allocations, err := t.q.AllocationsForTransactions(t.ctx, tenantdb.AllocationsForTransactionsParams{AccountID: t.accountID, TransactionIds: ids})
	if err != nil {
		return nil, fmt.Errorf("list allocations: %w", err)
	}
	for _, row := range allocations {
		if state := states[row.TransactionID]; state != nil && state.Category == nil {
			state.Category = &CategoryState{Code: row.Code, Label: row.Label, Status: AllocationStatus(row.Status)}
		}
	}
	return states, nil
}
