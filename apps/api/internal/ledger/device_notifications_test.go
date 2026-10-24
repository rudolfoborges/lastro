package ledger_test

import (
	"errors"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func TestDeviceNotificationsShowWhatCaseDidWithEachVersion(t *testing.T) {
	f := newFixture(t)
	purchase := walletPurchase("0|wallet|7|null|1", "R$ 32,97 com Visa •••• 1234")
	f.interpret(purchaseReading(f.walletObservation(purchase), brl(3297)))
	pending := walletPurchase("0|wallet|7|null|2", "R$ 10,00 com Visa •••• 1234")
	pending.ReceivedAt = purchase.ReceivedAt.Add(time.Minute)
	f.ingest(pending)
	otherPhone := walletPurchase("0|wallet|7|null|3", "R$ 5,00 com Visa •••• 1234")
	otherPhone.DeviceID = "another-phone"
	f.ingest(otherPhone)

	got, err := f.ledger.DeviceNotifications(t.Context(), f.account, "galaxy-s24", 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("notifications = %d, want the 2 from galaxy-s24", len(got))
	}
	if got[0].NotificationKey != pending.NotificationKey || got[0].Interpretation != nil || got[0].Transaction != nil {
		t.Errorf("newest = %+v, want the pending notification first, without interpretation", got[0])
	}
	interpreted := got[1]
	if interpreted.Interpretation == nil || interpreted.Interpretation.Effect != ledger.EffectCreated {
		t.Fatalf("interpreted = %+v, want the created purchase", interpreted.Interpretation)
	}
	if interpreted.Transaction == nil || interpreted.Transaction.Status != ledger.StatusProvisional {
		t.Fatalf("transaction = %+v, want a provisional credit card purchase", interpreted.Transaction)
	}
	if c := interpreted.Transaction.Category; c == nil || c.Code != "meals" || c.Label != "Alimentação e refeições" {
		t.Errorf("category = %+v, want meals", c)
	}
}

func TestDeviceNotificationsOfAnUnknownDeviceAreEmpty(t *testing.T) {
	f := newFixture(t)
	f.ingest(walletPurchase("0|wallet|7|null|1", "R$ 32,97"))

	got, err := f.ledger.DeviceNotifications(t.Context(), f.newAccount("Outra"), "galaxy-s24", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("notifications of another account = %+v, want none", got)
	}
}

func TestInvalidDeviceNotificationRequestsAreRejected(t *testing.T) {
	f := newFixture(t)
	for name, call := range map[string]struct {
		device string
		limit  int
	}{
		"missing device": {"", 10},
		"invalid device": {"galaxy s24", 10},
		"limit too high": {"galaxy-s24", 101},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.ledger.DeviceNotifications(t.Context(), f.account, call.device, call.limit)
			var validation *ledger.ValidationError
			if !errors.As(err, &validation) {
				t.Errorf("error = %v, want a validation error", err)
			}
		})
	}
}
