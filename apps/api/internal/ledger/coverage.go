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
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	HeartbeatGap         = time.Hour
	LateDelivery         = 15 * time.Minute
	DefaultInboxInterval = 6 * time.Hour
	minInboxInterval     = 15 * time.Minute
	maxInboxInterval     = 7 * 24 * time.Hour
	maxCoverageSpan      = 92 * 24 * time.Hour
	toolListPending      = "list_pending_notifications"
	localTimeLayout      = "02/01 15:04"
)

var clientVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,50}$`)

type TrackingState string

const (
	TrackingActive       TrackingState = "active"
	TrackingPaused       TrackingState = "paused"
	TrackingNeverEnabled TrackingState = "never_enabled"
)

type Tracking struct {
	State         TrackingState
	Since         *time.Time
	InboxInterval time.Duration
}

type Heartbeat struct {
	DeviceID            string
	SentAt              time.Time
	ClientVersion       string
	NotificationAccess  bool
	BatteryUnrestricted *bool
	QueueSize           *int
	ReceivedAt          time.Time
}

type Window struct {
	From time.Time
	To   time.Time
	Open bool
}

type DeviceStatus struct {
	DeviceID            string
	LastContactAt       time.Time
	LastHeartbeatAt     *time.Time
	ClientVersion       *string
	NotificationAccess  *bool
	BatteryUnrestricted *bool
	QueueSize           *int
	DiscardedCount      int64
}

type InboxStatus struct {
	Pending         int
	OldestPendingAt *time.Time
	LastProcessedAt *time.Time
	Late            bool
}

type Coverage struct {
	From           time.Time
	To             time.Time
	Tracking       Tracking
	Devices        []DeviceStatus
	Gaps           []Window
	Inactive       []Window
	LateDeliveries int
	WorstDelay     time.Duration
	Inbox          InboxStatus
	Complete       bool
	Limitations    []string
	Location       *time.Location
}

func (s *Service) RecordHeartbeat(ctx context.Context, accountID uuid.UUID, in Heartbeat) (Tracking, error) {
	if in.ReceivedAt.IsZero() {
		in.ReceivedAt = time.Now()
	}
	if err := validateHeartbeat(in); err != nil {
		return Tracking{}, err
	}
	var tracking Tracking
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
		params := tenantdb.InsertHeartbeatParams{
			ID:                  store.NewID(),
			AccountID:           t.accountID,
			WalletDeviceID:      device.ID,
			ReceivedAt:          in.ReceivedAt.UTC(),
			SentAt:              in.SentAt.UTC(),
			ClientVersion:       in.ClientVersion,
			NotificationAccess:  in.NotificationAccess,
			BatteryUnrestricted: in.BatteryUnrestricted,
		}
		if in.QueueSize != nil {
			size := int32(*in.QueueSize) //nolint:gosec // bounded by validation
			params.QueueSize = &size
		}
		if _, err := t.q.InsertHeartbeat(t.ctx, params); err != nil {
			return fmt.Errorf("insert heartbeat: %w", err)
		}
		tracking, err = t.tracking()
		return err
	})
	return tracking, err
}

func validateHeartbeat(in Heartbeat) error {
	switch {
	case !devicePattern.MatchString(in.DeviceID):
		return &ValidationError{Field: "device_id", Message: "must have 1 to 100 letters, digits, '.', '_', ':' or '-'"}
	case !clientVersionPattern.MatchString(in.ClientVersion):
		return &ValidationError{Field: "client_version", Message: "must have 1 to 50 letters, digits, '.', '_', '+' or '-'"}
	case in.QueueSize != nil && (*in.QueueSize < 0 || *in.QueueSize > 1_000_000):
		return &ValidationError{Field: "queue_size", Message: "must be between 0 and 1000000"}
	}
	return nil
}

func (s *Service) SetTracking(ctx context.Context, accountID uuid.UUID, active bool, inboxInterval *time.Duration, now time.Time) (Tracking, error) {
	if inboxInterval != nil && (*inboxInterval < minInboxInterval || *inboxInterval > maxInboxInterval) {
		return Tracking{}, &ValidationError{Field: "inbox_interval_minutes", Message: "must be between 15 minutes and 7 days"}
	}
	var tracking Tracking
	err := s.inTx(ctx, accountID, func(t *tx) error {
		open, err := t.q.OpenTrackingPeriod(t.ctx, t.accountID)
		switch {
		case errors.Is(err, pgx.ErrNoRows) && active:
			interval := DefaultInboxInterval
			if latest, err := t.q.LatestTrackingPeriod(t.ctx, t.accountID); err == nil {
				interval = time.Duration(latest.InboxIntervalMinutes) * time.Minute
			}
			if inboxInterval != nil {
				interval = *inboxInterval
			}
			_, err = t.q.StartTrackingPeriod(t.ctx, tenantdb.StartTrackingPeriodParams{
				ID:                   store.NewID(),
				AccountID:            t.accountID,
				StartedAt:            now.UTC(),
				InboxIntervalMinutes: intervalMinutes(interval),
			})
			if err != nil {
				return fmt.Errorf("start tracking: %w", err)
			}
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("find tracking period: %w", err)
		case !active:
			endedAt := now.UTC()
			if _, err := t.q.EndTrackingPeriod(t.ctx, tenantdb.EndTrackingPeriodParams{AccountID: t.accountID, ID: open.ID, EndedAt: &endedAt}); err != nil {
				return fmt.Errorf("pause tracking: %w", err)
			}
		case inboxInterval != nil:
			params := tenantdb.SetInboxIntervalParams{AccountID: t.accountID, ID: open.ID, InboxIntervalMinutes: intervalMinutes(*inboxInterval)}
			if _, err := t.q.SetInboxInterval(t.ctx, params); err != nil {
				return fmt.Errorf("set inbox interval: %w", err)
			}
		}
		tracking, err = t.tracking()
		return err
	})
	return tracking, err
}

func intervalMinutes(interval time.Duration) int32 {
	return int32(interval / time.Minute) //nolint:gosec // validated between 15 minutes and 7 days
}

func (t *tx) tracking() (Tracking, error) {
	latest, err := t.q.LatestTrackingPeriod(t.ctx, t.accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tracking{State: TrackingNeverEnabled, InboxInterval: DefaultInboxInterval}, nil
	}
	if err != nil {
		return Tracking{}, fmt.Errorf("latest tracking period: %w", err)
	}
	tracking := Tracking{State: TrackingActive, Since: &latest.StartedAt, InboxInterval: time.Duration(latest.InboxIntervalMinutes) * time.Minute}
	if latest.EndedAt != nil {
		tracking.State, tracking.Since = TrackingPaused, latest.EndedAt
	}
	return tracking, nil
}

func (s *Service) Coverage(ctx context.Context, accountID uuid.UUID, from, to, now time.Time) (Coverage, error) {
	if to.After(now) {
		to = now
	}
	if !from.Before(to) {
		return Coverage{}, &ValidationError{Field: "from", Message: "must be before to and before now"}
	}
	if to.Sub(from) > maxCoverageSpan {
		return Coverage{}, &ValidationError{Field: "to", Message: "the period can have at most 92 days"}
	}
	coverage := Coverage{From: from, To: to}
	err := s.inTx(ctx, accountID, func(t *tx) error {
		c := &coverageBuilder{t: t, coverage: &coverage, now: now}
		return c.build()
	})
	return coverage, err
}

type coverageBuilder struct {
	t        *tx
	coverage *Coverage
	now      time.Time
}

type activeWindow struct {
	Window
	enabledAt bool
}

func (c *coverageBuilder) build() error {
	var err error
	if c.coverage.Location, err = c.t.location(); err != nil {
		return err
	}
	if c.coverage.Tracking, err = c.t.tracking(); err != nil {
		return err
	}
	active, err := c.activeWindows()
	if err != nil {
		return err
	}
	c.coverage.Inactive = c.inactiveWindows(active)
	for _, window := range active {
		gaps, err := c.gapsIn(window)
		if err != nil {
			return err
		}
		c.coverage.Gaps = append(c.coverage.Gaps, gaps...)
	}
	if err := c.lateDeliveries(); err != nil {
		return err
	}
	if err := c.devices(); err != nil {
		return err
	}
	if err := c.inbox(); err != nil {
		return err
	}
	c.limitations()
	return nil
}

func (c *coverageBuilder) activeWindows() ([]activeWindow, error) {
	periods, err := c.t.q.TrackingPeriodsBetween(c.t.ctx, tenantdb.TrackingPeriodsBetweenParams{AccountID: c.t.accountID, Since: c.coverage.From, Until: c.coverage.To})
	if err != nil {
		return nil, fmt.Errorf("list tracking periods: %w", err)
	}
	windows := make([]activeWindow, 0, len(periods))
	for _, period := range periods {
		window := activeWindow{Window: Window{From: period.StartedAt, To: c.coverage.To, Open: period.EndedAt == nil}, enabledAt: true}
		if period.EndedAt != nil && period.EndedAt.Before(window.To) {
			window.To = *period.EndedAt
		}
		if window.From.Before(c.coverage.From) {
			window.From, window.enabledAt = c.coverage.From, false
		}
		windows = append(windows, window)
	}
	return windows, nil
}

func (c *coverageBuilder) inactiveWindows(active []activeWindow) []Window {
	var inactive []Window
	cursor := c.coverage.From
	for _, window := range active {
		if window.From.After(cursor) {
			inactive = append(inactive, Window{From: cursor, To: window.From})
		}
		cursor = window.To
	}
	if cursor.Before(c.coverage.To) {
		inactive = append(inactive, Window{From: cursor, To: c.coverage.To, Open: c.coverage.Tracking.State != TrackingActive})
	}
	return inactive
}

// A window is covered while the last sign of life is less than HeartbeatGap
// old; enabling the tracking counts as a sign of life so the first heartbeat
// has time to arrive.
func (c *coverageBuilder) gapsIn(window activeWindow) ([]Window, error) {
	beats, err := c.t.q.HeartbeatTimesBetween(c.t.ctx, tenantdb.HeartbeatTimesBetweenParams{AccountID: c.t.accountID, Since: window.From, Until: window.To})
	if err != nil {
		return nil, fmt.Errorf("list heartbeats: %w", err)
	}
	last := window.From
	known := window.enabledAt
	if !known {
		before, err := c.t.q.LastHeartbeatBefore(c.t.ctx, tenantdb.LastHeartbeatBeforeParams{AccountID: c.t.accountID, Until: window.From})
		switch {
		case err == nil:
			last, known = before, true
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("last heartbeat: %w", err)
		}
	}
	var gaps []Window
	for _, beat := range beats {
		if !known || beat.Sub(last) > HeartbeatGap {
			gaps = append(gaps, Window{From: latest(last, window.From), To: beat})
		}
		last, known = beat, true
	}
	if !known || window.To.Sub(last) > HeartbeatGap {
		gaps = append(gaps, Window{From: latest(last, window.From), To: window.To, Open: window.Open && !window.To.Before(c.now)})
	}
	return gaps, nil
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (c *coverageBuilder) lateDeliveries() error {
	row, err := c.t.q.LateDeliveries(c.t.ctx, tenantdb.LateDeliveriesParams{
		AccountID:        c.t.accountID,
		Since:            c.coverage.From,
		Until:            c.coverage.To,
		ThresholdSeconds: int64(LateDelivery / time.Second),
	})
	if err != nil {
		return fmt.Errorf("count late deliveries: %w", err)
	}
	c.coverage.LateDeliveries, c.coverage.WorstDelay = int(row.Late), time.Duration(row.WorstSeconds)*time.Second
	return nil
}

func (c *coverageBuilder) devices() error {
	devices, err := c.t.q.ListWalletDevices(c.t.ctx, c.t.accountID)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	beats, err := c.t.q.LatestHeartbeats(c.t.ctx, c.t.accountID)
	if err != nil {
		return fmt.Errorf("latest heartbeats: %w", err)
	}
	latestBeat := make(map[uuid.UUID]tenantdb.WalletHeartbeat, len(beats))
	for _, beat := range beats {
		latestBeat[beat.WalletDeviceID] = beat
	}
	for _, device := range devices {
		status := DeviceStatus{DeviceID: device.DeviceID, LastContactAt: device.LastReceivedAt, DiscardedCount: device.DiscardedCount}
		if beat, ok := latestBeat[device.ID]; ok {
			status.LastHeartbeatAt, status.ClientVersion = &beat.ReceivedAt, &beat.ClientVersion
			status.NotificationAccess, status.BatteryUnrestricted = &beat.NotificationAccess, beat.BatteryUnrestricted
			if beat.QueueSize != nil {
				size := int(*beat.QueueSize)
				status.QueueSize = &size
			}
		}
		c.coverage.Devices = append(c.coverage.Devices, status)
	}
	return nil
}

func (c *coverageBuilder) inbox() error {
	pending, err := c.t.q.CountPendingNotifications(c.t.ctx, c.t.accountID)
	if err != nil {
		return fmt.Errorf("count pending notifications: %w", err)
	}
	inbox := InboxStatus{Pending: int(pending)}
	oldest, err := c.t.q.OldestPendingNotification(c.t.ctx, c.t.accountID)
	switch {
	case err == nil:
		inbox.OldestPendingAt = &oldest
		inbox.Late = c.now.Sub(oldest) > 2*c.coverage.Tracking.InboxInterval
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("oldest pending notification: %w", err)
	}
	processed, err := c.t.q.LastToolCall(c.t.ctx, tenantdb.LastToolCallParams{AccountID: c.t.accountID, Tool: toolListPending})
	switch {
	case err == nil:
		inbox.LastProcessedAt = &processed
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("last inbox processing: %w", err)
	}
	c.coverage.Inbox = inbox
	return nil
}

func (c *coverageBuilder) limitations() {
	cov := c.coverage
	at := func(t time.Time) string { return t.In(cov.Location).Format(localTimeLayout) }
	var out []string
	if cov.Tracking.State == TrackingNeverEnabled {
		out = append(out, "O acompanhamento automático do Google Wallet nunca foi ativado: nenhuma compra entra sozinha.")
	} else {
		for _, window := range cov.Inactive {
			if window.Open {
				out = append(out, fmt.Sprintf("O acompanhamento automático está pausado desde %s.", at(window.From)))
			} else {
				out = append(out, fmt.Sprintf("O acompanhamento automático esteve pausado ou desativado de %s a %s.", at(window.From), at(window.To)))
			}
		}
	}
	for _, gap := range cov.Gaps {
		if gap.Open {
			out = append(out, fmt.Sprintf("Sem sinal do celular desde %s: compras feitas desde então podem não ter chegado.", at(gap.From)))
		} else {
			out = append(out, fmt.Sprintf("Sem sinal do celular de %s a %s: compras desse intervalo podem não ter chegado.", at(gap.From), at(gap.To)))
		}
	}
	if anyWithoutNotificationAccess(cov.Devices) {
		out = append(out, "O celular está sem permissão para ler notificações: nada é capturado até ela voltar.")
	}
	if cov.LateDeliveries > 0 {
		out = append(out, fmt.Sprintf("%d notificação(ões) chegaram com atraso (até %d min).", cov.LateDeliveries, int(cov.WorstDelay.Minutes())))
	}
	if cov.Inbox.Pending > 0 {
		line := fmt.Sprintf("%d notificação(ões) ainda não foram interpretadas pela Case e não entram nos totais", cov.Inbox.Pending)
		if cov.Inbox.Late && cov.Inbox.OldestPendingAt != nil {
			line += fmt.Sprintf("; a mais antiga espera desde %s", at(*cov.Inbox.OldestPendingAt))
		}
		out = append(out, line+".")
	}
	cov.Limitations = out
	cov.Complete = cov.Tracking.State != TrackingNeverEnabled && len(cov.Inactive) == 0 && len(cov.Gaps) == 0 && cov.Inbox.Pending == 0 &&
		!anyWithoutNotificationAccess(cov.Devices)
}

func anyWithoutNotificationAccess(devices []DeviceStatus) bool {
	for _, device := range devices {
		if device.NotificationAccess != nil && !*device.NotificationAccess {
			return true
		}
	}
	return false
}

func (t *tx) trackingActive() (bool, error) {
	active, err := t.q.IsTrackingActive(t.ctx, t.accountID)
	if err != nil {
		return false, fmt.Errorf("check tracking: %w", err)
	}
	return active, nil
}

func (s *Service) CoverageForDays(ctx context.Context, accountID uuid.UUID, from, to *calendar.Date, now time.Time) (Coverage, error) {
	var loc *time.Location
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		loc, err = t.location()
		return err
	})
	if err != nil {
		return Coverage{}, err
	}
	last := calendar.DateOf(now, loc)
	if to != nil {
		last = *to
	}
	first := last.AddDays(-6)
	if from != nil {
		first = *from
	}
	if last.Before(first) {
		return Coverage{}, &ValidationError{Field: "to", Message: "must not be before from"}
	}
	return s.Coverage(ctx, accountID, startOfDay(first, loc), startOfDay(last.AddDays(1), loc), now)
}

func startOfDay(day calendar.Date, loc *time.Location) time.Time {
	return time.Date(day.Year, day.Month, day.Day, 0, 0, 0, 0, loc)
}
