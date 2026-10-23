package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

const sentAtWindow = 5 * time.Minute

type walletNotificationRequest struct {
	DeviceID        string     `json:"device_id"`
	Package         string     `json:"package"`
	NotificationKey string     `json:"notification_key"`
	Event           string     `json:"event"`
	PostedAt        *time.Time `json:"posted_at"`
	Title           string     `json:"title"`
	Text            string     `json:"text"`
	BigText         string     `json:"big_text"`
	SentAt          *time.Time `json:"sent_at"`
}

type walletNotificationResponse struct {
	Result         ledger.IngestResult `json:"result"`
	NotificationID *uuid.UUID          `json:"notification_id,omitempty"`
	Version        int                 `json:"version,omitempty"`
}

func (a *api) handleWalletNotification(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	var body walletNotificationRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	receivedAt := time.Now()
	if !withinSentAtWindow(body.SentAt, receivedAt) {
		writeErrorMessage(w, http.StatusBadRequest, "stale_request",
			"sent_at must be the device time when the request is sent, within 5 minutes of the server time")
		return
	}
	in := ledger.WalletNotification{
		DeviceID:        body.DeviceID,
		Package:         body.Package,
		NotificationKey: body.NotificationKey,
		Event:           ledger.NotificationEvent(body.Event),
		Title:           body.Title,
		Text:            body.Text,
		BigText:         body.BigText,
		ReceivedAt:      receivedAt,
	}
	if body.PostedAt != nil {
		in.PostedAt = *body.PostedAt
	}
	receipt, err := a.ledger.IngestNotification(r.Context(), principal.AccountID, in)
	var validation *ledger.ValidationError
	switch {
	case errors.As(err, &validation):
		writeErrorMessage(w, http.StatusBadRequest, "invalid_request", validation.Error())
		return
	case err != nil:
		a.writeInternalError(w, r, err)
		return
	}
	status := http.StatusOK
	if receipt.Result == ledger.IngestStored {
		status = http.StatusCreated
	}
	writeJSON(w, status, walletNotificationResponse{
		Result:         receipt.Result,
		NotificationID: receipt.NotificationID,
		Version:        receipt.Version,
	})
}

func withinSentAtWindow(sentAt *time.Time, now time.Time) bool {
	if sentAt == nil {
		return false
	}
	skew := now.Sub(*sentAt)
	return skew <= sentAtWindow && skew >= -sentAtWindow
}

type heartbeatRequest struct {
	DeviceID            string     `json:"device_id"`
	ClientVersion       string     `json:"client_version"`
	NotificationAccess  *bool      `json:"notification_access"`
	BatteryUnrestricted *bool      `json:"battery_unrestricted"`
	QueueSize           *int       `json:"queue_size"`
	SentAt              *time.Time `json:"sent_at"`
}

type heartbeatResponse struct {
	Tracking   ledger.TrackingState `json:"tracking"`
	ServerTime time.Time            `json:"server_time"`
}

func (a *api) handleWalletHeartbeat(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	var body heartbeatRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	receivedAt := time.Now()
	if !withinSentAtWindow(body.SentAt, receivedAt) {
		writeErrorMessage(w, http.StatusBadRequest, "stale_request",
			"sent_at must be the device time when the request is sent, within 5 minutes of the server time")
		return
	}
	if body.NotificationAccess == nil {
		writeErrorMessage(w, http.StatusBadRequest, "invalid_request", "notification_access is required")
		return
	}
	tracking, err := a.ledger.RecordHeartbeat(r.Context(), principal.AccountID, ledger.Heartbeat{
		DeviceID:            body.DeviceID,
		SentAt:              *body.SentAt,
		ClientVersion:       body.ClientVersion,
		NotificationAccess:  *body.NotificationAccess,
		BatteryUnrestricted: body.BatteryUnrestricted,
		QueueSize:           body.QueueSize,
		ReceivedAt:          receivedAt,
	})
	var validation *ledger.ValidationError
	switch {
	case errors.As(err, &validation):
		writeErrorMessage(w, http.StatusBadRequest, "invalid_request", validation.Error())
	case err != nil:
		a.writeInternalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, heartbeatResponse{Tracking: tracking.State, ServerTime: receivedAt.UTC()})
	}
}
