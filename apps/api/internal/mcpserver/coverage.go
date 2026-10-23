package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

const outsideWalletNote = "Compras fora do Google Wallet (Pix, dinheiro, cartões fora da carteira) só entram pela conversa ou pela fatura."

type GetCoverageStatusInput struct {
	From string `json:"from,omitempty" jsonschema:"first day, YYYY-MM-DD; 6 days before to when omitted"`
	To   string `json:"to,omitempty" jsonschema:"last day, YYYY-MM-DD; today when omitted"`
}

type TrackingOutput struct {
	State                string  `json:"state" jsonschema:"active, paused or never_enabled"`
	Since                *string `json:"since,omitempty"`
	InboxIntervalMinutes int     `json:"inbox_interval_minutes" jsonschema:"how often the inbox is expected to be processed; pending notifications older than twice this are late"`
}

type CoverageWindowOutput struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Open    bool   `json:"open,omitempty" jsonschema:"still going on"`
	Minutes int    `json:"minutes"`
}

type InboxStatusOutput struct {
	Pending         int     `json:"pending"`
	OldestPendingAt *string `json:"oldest_pending_at,omitempty"`
	LastProcessedAt *string `json:"last_processed_at,omitempty" jsonschema:"last time the inbox was listed"`
	Late            bool    `json:"late"`
}

type DeviceStatusOutput struct {
	DeviceID               string  `json:"device_id"`
	LastContactAt          string  `json:"last_contact_at"`
	LastHeartbeatAt        *string `json:"last_heartbeat_at,omitempty"`
	ClientVersion          *string `json:"client_version,omitempty"`
	NotificationAccess     *bool   `json:"notification_access,omitempty"`
	BatteryUnrestricted    *bool   `json:"battery_unrestricted,omitempty"`
	QueueSize              *int    `json:"queue_size,omitempty" jsonschema:"notifications waiting on the phone to be sent"`
	DiscardedNotifications int64   `json:"discarded_notifications" jsonschema:"notifications from other apps that reached Lastro and were dropped"`
}

type GetCoverageStatusOutput struct {
	From              string                 `json:"from"`
	To                string                 `json:"to"`
	Complete          bool                   `json:"complete" jsonschema:"true only when automatic tracking ran the whole period without gaps and nothing waits for interpretation"`
	Tracking          TrackingOutput         `json:"tracking"`
	Gaps              []CoverageWindowOutput `json:"gaps,omitempty" jsonschema:"periods without a sign of life from the phone while tracking was on"`
	Inactive          []CoverageWindowOutput `json:"inactive,omitempty" jsonschema:"periods with tracking paused or not yet enabled"`
	LateDeliveries    int                    `json:"late_deliveries"`
	WorstDelayMinutes int                    `json:"worst_delay_minutes,omitempty"`
	Inbox             InboxStatusOutput      `json:"inbox"`
	Devices           []DeviceStatusOutput   `json:"devices,omitempty"`
	Limitations       []string               `json:"limitations,omitempty" jsonschema:"what to tell Rudolfo before any total of this period, in Portuguese"`
	Summary           string                 `json:"summary"`
}

type SetTrackingInput struct {
	State                string `json:"state" jsonschema:"active to start or resume automatic tracking, paused to stop it"`
	InboxIntervalMinutes int    `json:"inbox_interval_minutes,omitempty" jsonschema:"how often Rudolfo's routine processes the inbox, from 15 to 10080 minutes"`
}

type SetTrackingOutput struct {
	Outcome  Outcome        `json:"outcome"`
	Tracking TrackingOutput `json:"tracking"`
	Summary  string         `json:"summary"`
}

func (o SetTrackingOutput) reportedOutcome() Outcome {
	return o.Outcome
}

func (s *Server) registerCoverage() {
	addTool(s, toolSpec{
		name:  "get_coverage_status",
		title: "Show how complete the automatic tracking is",
		description: "Shows, for a period of days, whether Google Wallet tracking was on, gaps without a sign of life from the phone, late notifications, the inbox waiting for you and the phone's permissions. " +
			"Read it before reporting spending of a period and tell Rudolfo every limitation: missing notifications never mean there was no spending.",
		scope:      accounts.ScopeExpensesRead,
		readOnly:   true,
		idempotent: true,
	}, func(ctx context.Context, c call, in GetCoverageStatusInput) (GetCoverageStatusOutput, error) {
		from, err := parseOptionalDate("from", in.From)
		if err != nil {
			return GetCoverageStatusOutput{}, err
		}
		to, err := parseOptionalDate("to", in.To)
		if err != nil {
			return GetCoverageStatusOutput{}, err
		}
		coverage, err := s.deps.Ledger.CoverageForDays(ctx, c.principal.AccountID, from, to, c.receivedAt)
		if err != nil {
			return GetCoverageStatusOutput{}, err
		}
		return coverageOutput(coverage), nil
	})

	addTool(s, toolSpec{
		name:        "set_tracking",
		title:       "Turn automatic tracking on or off",
		description: "Starts, pauses or resumes the automatic tracking of Google Wallet notifications. Use only when Rudolfo asks. While paused, the phone's notifications are dropped and everything already recorded stays available.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
		enums:       map[string][]any{"state": {"active", "paused"}},
	}, func(ctx context.Context, c call, in SetTrackingInput) (SetTrackingOutput, error) {
		if in.State != "active" && in.State != "paused" {
			return SetTrackingOutput{}, invalid("state", "must be active or paused")
		}
		var interval *time.Duration
		if in.InboxIntervalMinutes != 0 {
			value := time.Duration(in.InboxIntervalMinutes) * time.Minute
			interval = &value
		}
		tracking, err := s.deps.Ledger.SetTracking(ctx, c.principal.AccountID, in.State == "active", interval, c.receivedAt)
		if err != nil {
			return SetTrackingOutput{}, err
		}
		out := SetTrackingOutput{Outcome: OutcomeCompleted, Tracking: trackingOutput(tracking, nil)}
		if tracking.State == ledger.TrackingActive {
			out.Summary = fmt.Sprintf("Acompanhamento automático do Google Wallet ativo. A caixa de notificações é considerada atrasada após %d min sem processamento.", int(2*tracking.InboxInterval/time.Minute))
		} else {
			out.Summary = "Acompanhamento automático pausado: as notificações do celular são descartadas, e tudo o que já foi registrado continua disponível."
		}
		return out, nil
	})
}

func coverageOutput(c ledger.Coverage) GetCoverageStatusOutput {
	local := func(t time.Time) string { return t.In(c.Location).Format(time.RFC3339) }
	optional := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		value := local(*t)
		return &value
	}
	windows := func(in []ledger.Window) []CoverageWindowOutput {
		out := make([]CoverageWindowOutput, 0, len(in))
		for _, w := range in {
			out = append(out, CoverageWindowOutput{From: local(w.From), To: local(w.To), Open: w.Open, Minutes: int(w.To.Sub(w.From).Minutes())})
		}
		return out
	}
	out := GetCoverageStatusOutput{
		From:              local(c.From),
		To:                local(c.To),
		Complete:          c.Complete,
		Tracking:          trackingOutput(c.Tracking, c.Location),
		Gaps:              windows(c.Gaps),
		Inactive:          windows(c.Inactive),
		LateDeliveries:    c.LateDeliveries,
		WorstDelayMinutes: int(c.WorstDelay.Minutes()),
		Inbox: InboxStatusOutput{
			Pending:         c.Inbox.Pending,
			OldestPendingAt: optional(c.Inbox.OldestPendingAt),
			LastProcessedAt: optional(c.Inbox.LastProcessedAt),
			Late:            c.Inbox.Late,
		},
		Limitations: c.Limitations,
	}
	for _, device := range c.Devices {
		out.Devices = append(out.Devices, DeviceStatusOutput{
			DeviceID:               device.DeviceID,
			LastContactAt:          local(device.LastContactAt),
			LastHeartbeatAt:        optional(device.LastHeartbeatAt),
			ClientVersion:          device.ClientVersion,
			NotificationAccess:     device.NotificationAccess,
			BatteryUnrestricted:    device.BatteryUnrestricted,
			QueueSize:              device.QueueSize,
			DiscardedNotifications: device.DiscardedCount,
		})
	}
	period := fmt.Sprintf("de %s a %s", c.From.In(c.Location).Format("02/01 15:04"), c.To.In(c.Location).Format("02/01 15:04"))
	if c.Complete {
		out.Summary = "Acompanhamento do Google Wallet sem interrupções " + period + ". " + outsideWalletNote
	} else {
		out.Summary = "Cobertura incompleta " + period + ": " + strings.Join(c.Limitations, " ") + " " + outsideWalletNote
	}
	return out
}

func trackingOutput(t ledger.Tracking, loc *time.Location) TrackingOutput {
	out := TrackingOutput{State: string(t.State), InboxIntervalMinutes: int(t.InboxInterval / time.Minute)}
	if t.Since != nil {
		since := t.Since.UTC()
		if loc != nil {
			since = since.In(loc)
		}
		value := since.Format(time.RFC3339)
		out.Since = &value
	}
	return out
}
