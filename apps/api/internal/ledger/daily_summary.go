package ledger

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const DefaultDailySummaryTime = "08:00"

var timeOfDay = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type DailySummarySettings struct {
	Enabled bool
	Time    string
}

type DailySummaryDelivery struct {
	Day         calendar.Date
	DeliveredAt time.Time
	DeliveredBy string
}

func (s *Service) DailySummarySettings(ctx context.Context, accountID uuid.UUID) (DailySummarySettings, error) {
	var settings DailySummarySettings
	err := s.inTx(ctx, accountID, func(t *tx) error {
		account, err := t.q.GetAccount(t.ctx, t.accountID)
		if err != nil {
			return fmt.Errorf("get account: %w", err)
		}
		settings = DailySummarySettings{Enabled: account.DailySummaryEnabled, Time: account.DailySummaryTime}
		return nil
	})
	return settings, err
}

// SetDailySummary turns the daily summary on or off; Case only calls it when
// Rudolfo asks. Without a time, the current one is kept.
func (s *Service) SetDailySummary(ctx context.Context, accountID uuid.UUID, enabled bool, at *string) (DailySummarySettings, error) {
	if at != nil && !timeOfDay.MatchString(*at) {
		return DailySummarySettings{}, &ValidationError{Field: "time", Message: "must be HH:MM in 24 hours, such as 08:00"}
	}
	var settings DailySummarySettings
	err := s.inTx(ctx, accountID, func(t *tx) error {
		account, err := t.q.GetAccount(t.ctx, t.accountID)
		if err != nil {
			return fmt.Errorf("get account: %w", err)
		}
		next := account.DailySummaryTime
		if at != nil {
			next = *at
		}
		updated, err := t.q.UpdateDailySummarySettings(t.ctx, tenantdb.UpdateDailySummarySettingsParams{ID: t.accountID, Enabled: enabled, At: next})
		if err != nil {
			return fmt.Errorf("update daily summary: %w", err)
		}
		settings = DailySummarySettings{Enabled: updated.DailySummaryEnabled, Time: updated.DailySummaryTime}
		return nil
	})
	return settings, err
}

func (s *Service) DailySummaryDelivery(ctx context.Context, accountID uuid.UUID, day calendar.Date) (*DailySummaryDelivery, error) {
	var delivery *DailySummaryDelivery
	err := s.inTx(ctx, accountID, func(t *tx) error {
		row, err := t.q.GetDailySummaryDelivery(t.ctx, tenantdb.GetDailySummaryDeliveryParams{AccountID: t.accountID, Day: day.Time()})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get daily summary delivery: %w", err)
		}
		delivery = toDelivery(row)
		return nil
	})
	return delivery, err
}

// MarkDailySummaryDelivered records that Case sent the summary of a day, so
// it is never sent twice. The second mark keeps the first delivery.
func (s *Service) MarkDailySummaryDelivered(ctx context.Context, accountID uuid.UUID, day calendar.Date, now time.Time, actor Actor) (DailySummaryDelivery, bool, error) {
	var delivery DailySummaryDelivery
	var already bool
	err := s.inTx(ctx, accountID, func(t *tx) error {
		row, err := t.q.InsertDailySummaryDelivery(t.ctx, tenantdb.InsertDailySummaryDeliveryParams{
			AccountID: t.accountID, Day: day.Time(), DeliveredAt: now.UTC(), DeliveredBy: actor.Ref,
		})
		if err == nil {
			delivery = *toDelivery(row)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("mark daily summary delivered: %w", err)
		}
		existing, err := t.q.GetDailySummaryDelivery(t.ctx, tenantdb.GetDailySummaryDeliveryParams{AccountID: t.accountID, Day: day.Time()})
		if err != nil {
			return fmt.Errorf("get daily summary delivery: %w", err)
		}
		delivery, already = *toDelivery(existing), true
		return nil
	})
	return delivery, already, err
}

func toDelivery(row tenantdb.DailySummaryDelivery) *DailySummaryDelivery {
	return &DailySummaryDelivery{Day: calendar.DateFromTime(row.Day), DeliveredAt: row.DeliveredAt, DeliveredBy: row.DeliveredBy}
}
