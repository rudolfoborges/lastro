package ledger_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

var enabledAt = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func (f *fixture) setTracking(active bool, at time.Time, interval *time.Duration) {
	f.t.Helper()
	if _, err := f.ledger.SetTracking(f.t.Context(), f.account, active, interval, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) beat(from, to time.Time) {
	f.t.Helper()
	for at := from; !at.After(to); at = at.Add(15 * time.Minute) {
		_, err := f.ledger.RecordHeartbeat(f.t.Context(), f.account, ledger.Heartbeat{
			DeviceID:           "galaxy-s24",
			SentAt:             at,
			ClientVersion:      "0.1.0",
			NotificationAccess: true,
			ReceivedAt:         at,
		})
		if err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) coverage(from, to, now time.Time) ledger.Coverage {
	f.t.Helper()
	coverage, err := f.ledger.Coverage(f.t.Context(), f.account, from, to, now)
	if err != nil {
		f.t.Fatalf("Coverage() error = %v", err)
	}
	return coverage
}

func mentions(limitations []string, text string) bool {
	for _, limitation := range limitations {
		if strings.Contains(limitation, text) {
			return true
		}
	}
	return false
}

func TestAStoppedHeartbeatBecomesAGapWithinAnHour(t *testing.T) {
	f := newFixture(t)
	f.setTracking(true, enabledAt, nil)
	last := enabledAt.Add(2 * time.Hour)
	f.beat(enabledAt, last)

	healthy := f.coverage(enabledAt, last.Add(time.Hour), last.Add(59*time.Minute))
	silent := f.coverage(enabledAt, last.Add(2*time.Hour), last.Add(61*time.Minute))

	if len(healthy.Gaps) != 0 || !healthy.Complete || len(healthy.Limitations) != 0 {
		t.Errorf("59 minutes after the last heartbeat = %+v, want complete coverage", healthy)
	}
	if len(silent.Gaps) != 1 || !silent.Gaps[0].Open || !silent.Gaps[0].From.Equal(last) || silent.Complete {
		t.Fatalf("61 minutes after the last heartbeat = %+v, want an open gap since %s", silent.Gaps, last)
	}
	if !mentions(silent.Limitations, "Sem sinal do celular desde 22/10 11:00") {
		t.Errorf("limitations = %v, want the open gap in São Paulo time", silent.Limitations)
	}
}

func TestAcceptanceCriterion11CoverageOfAPeriodWithAGap(t *testing.T) {
	f := newFixture(t)
	f.setTracking(true, enabledAt, nil)
	f.beat(enabledAt, enabledAt.Add(time.Hour))
	f.beat(enabledAt.Add(4*time.Hour), enabledAt.Add(6*time.Hour))

	coverage := f.coverage(enabledAt, enabledAt.Add(6*time.Hour), enabledAt.Add(6*time.Hour))

	if coverage.Complete {
		t.Fatal("a period with a gap is reported as complete")
	}
	if len(coverage.Gaps) != 1 || coverage.Gaps[0].Open || !coverage.Gaps[0].From.Equal(enabledAt.Add(time.Hour)) || !coverage.Gaps[0].To.Equal(enabledAt.Add(4*time.Hour)) {
		t.Errorf("gaps = %+v, want 13:00 to 16:00 UTC", coverage.Gaps)
	}
	if !mentions(coverage.Limitations, "Sem sinal do celular de 22/10 10:00 a 22/10 13:00") {
		t.Errorf("limitations = %v, want the gap explained", coverage.Limitations)
	}
}

func TestAPausedPeriodIsNotAGap(t *testing.T) {
	f := newFixture(t)
	f.setTracking(true, enabledAt, nil)
	f.beat(enabledAt, enabledAt.Add(time.Hour))
	f.setTracking(false, enabledAt.Add(time.Hour), nil)
	f.setTracking(true, enabledAt.Add(5*time.Hour), nil)
	f.beat(enabledAt.Add(5*time.Hour), enabledAt.Add(6*time.Hour))

	coverage := f.coverage(enabledAt, enabledAt.Add(6*time.Hour), enabledAt.Add(6*time.Hour))

	if len(coverage.Gaps) != 0 {
		t.Errorf("gaps = %+v, want none: the tracking was paused", coverage.Gaps)
	}
	if len(coverage.Inactive) != 1 || !coverage.Inactive[0].From.Equal(enabledAt.Add(time.Hour)) || coverage.Complete {
		t.Errorf("inactive = %+v, want the pause reported", coverage.Inactive)
	}
	if !mentions(coverage.Limitations, "esteve pausado") {
		t.Errorf("limitations = %v", coverage.Limitations)
	}
}

func TestCoverageBeforeTrackingIsEnabled(t *testing.T) {
	f := newFixture(t)

	coverage := f.coverage(enabledAt, enabledAt.Add(time.Hour), enabledAt.Add(time.Hour))

	if coverage.Tracking.State != ledger.TrackingNeverEnabled || coverage.Complete || !mentions(coverage.Limitations, "nunca foi ativado") {
		t.Errorf("coverage = %+v, want tracking never enabled as the limitation", coverage)
	}
}

func TestLateDeliveriesAndAWaitingInboxAreReported(t *testing.T) {
	f := newFixture(t)
	interval := 15 * time.Minute
	f.setTracking(true, enabledAt, &interval)
	f.beat(enabledAt, enabledAt.Add(2*time.Hour))
	notification := walletPurchase("0|wallet|7|null|10123", "R$ 32,97")
	notification.PostedAt, notification.ReceivedAt = enabledAt.Add(50*time.Minute), enabledAt.Add(80*time.Minute)
	f.ingest(notification)

	coverage := f.coverage(enabledAt, enabledAt.Add(2*time.Hour), enabledAt.Add(2*time.Hour))

	if coverage.LateDeliveries != 1 || coverage.WorstDelay != 30*time.Minute {
		t.Errorf("late deliveries = %d (worst %s), want 1 of 30m", coverage.LateDeliveries, coverage.WorstDelay)
	}
	if coverage.Inbox.Pending != 1 || !coverage.Inbox.Late || coverage.Complete {
		t.Errorf("inbox = %+v, want one notification waiting too long", coverage.Inbox)
	}
	if !mentions(coverage.Limitations, "ainda não foram interpretadas") || !mentions(coverage.Limitations, "chegaram com atraso") {
		t.Errorf("limitations = %v", coverage.Limitations)
	}
}

func TestDevicesReportTheirLastHeartbeatAndPermissions(t *testing.T) {
	f := newFixture(t)
	f.setTracking(true, enabledAt, nil)
	queue := 3
	_, err := f.ledger.RecordHeartbeat(t.Context(), f.account, ledger.Heartbeat{
		DeviceID:           "galaxy-s24",
		SentAt:             enabledAt,
		ClientVersion:      "0.1.0",
		NotificationAccess: false,
		QueueSize:          &queue,
		ReceivedAt:         enabledAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	coverage := f.coverage(enabledAt, enabledAt.Add(30*time.Minute), enabledAt.Add(30*time.Minute))

	if len(coverage.Devices) != 1 || *coverage.Devices[0].NotificationAccess || *coverage.Devices[0].QueueSize != 3 {
		t.Fatalf("devices = %+v", coverage.Devices)
	}
	if coverage.Complete || !mentions(coverage.Limitations, "sem permissão para ler notificações") {
		t.Errorf("coverage = %+v, want the missing permission as a limitation", coverage)
	}
}

func TestInvalidCoverageRequestsAreRejected(t *testing.T) {
	f := newFixture(t)

	_, err := f.ledger.Coverage(t.Context(), f.account, enabledAt, enabledAt.Add(-time.Hour), enabledAt)
	assertValidation(t, err, "from")
	_, err = f.ledger.Coverage(t.Context(), f.account, enabledAt.Add(-100*24*time.Hour), enabledAt, enabledAt)
	assertValidation(t, err, "to")
	tooShort := 5 * time.Minute
	_, err = f.ledger.SetTracking(t.Context(), f.account, true, &tooShort, enabledAt)
	assertValidation(t, err, "inbox_interval_minutes")
}
