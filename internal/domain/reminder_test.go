package domain

import (
	"errors"
	"testing"
	"time"
)

var testNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func TestPlanRemindersFull(t *testing.T) {
	d := Deadline{ID: 7, DueAt: testNow.Add(10 * 24 * time.Hour)}
	got := PlanReminders(d, []time.Duration{7 * 24 * time.Hour, 3 * 24 * time.Hour, 24 * time.Hour}, testNow)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantOffsets := []int{7 * 24 * 60, 3 * 24 * 60, 24 * 60}
	for i, r := range got {
		if r.DeadlineID != 7 {
			t.Errorf("[%d] DeadlineID = %d, want 7", i, r.DeadlineID)
		}
		if r.Kind != KindPreset {
			t.Errorf("[%d] Kind = %q, want %q", i, r.Kind, KindPreset)
		}
		if r.Status != ReminderStatusPending {
			t.Errorf("[%d] Status = %q, want pending", i, r.Status)
		}
		if r.OffsetMinutes == nil || *r.OffsetMinutes != wantOffsets[i] {
			t.Errorf("[%d] OffsetMinutes = %v, want %d", i, r.OffsetMinutes, wantOffsets[i])
		}
		wantFire := d.DueAt.Add(-time.Duration(wantOffsets[i]) * time.Minute)
		if !r.FireAt.Equal(wantFire) {
			t.Errorf("[%d] FireAt = %v, want %v", i, r.FireAt, wantFire)
		}
	}
}

func TestPlanRemindersDropsPast(t *testing.T) {
	d := Deadline{ID: 1, DueAt: testNow.Add(12 * time.Hour)}
	got := PlanReminders(d, []time.Duration{7 * 24 * time.Hour, 3 * 24 * time.Hour, 24 * time.Hour}, testNow)
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0 (all fire_at in past)", len(got))
	}
}

func TestPlanRemindersDropsSomePast(t *testing.T) {
	d := Deadline{ID: 1, DueAt: testNow.Add(2 * 24 * time.Hour)}
	got := PlanReminders(d, []time.Duration{7 * 24 * time.Hour, 3 * 24 * time.Hour, 24 * time.Hour}, testNow)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (only 24h preset fires in future)", len(got))
	}
	if got[0].OffsetMinutes == nil || *got[0].OffsetMinutes != 1440 {
		t.Errorf("OffsetMinutes = %v, want 1440", got[0].OffsetMinutes)
	}
}

func TestPlanRemindersDropsFireAtEqualNow(t *testing.T) {
	d := Deadline{ID: 1, DueAt: testNow.Add(24 * time.Hour)}
	got := PlanReminders(d, []time.Duration{24 * time.Hour}, testNow)
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0 (fire_at == now is dropped)", len(got))
	}
}

func TestPlanRemindersDedupes(t *testing.T) {
	d := Deadline{ID: 1, DueAt: testNow.Add(10 * 24 * time.Hour)}
	got := PlanReminders(d, []time.Duration{24 * time.Hour, 24 * time.Hour, 3 * 24 * time.Hour}, testNow)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (duplicate offsets collapsed)", len(got))
	}
}

func TestNewCustomOffsetReminder(t *testing.T) {
	r, ok := NewCustomOffsetReminder(5, time.Hour, testNow)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if r.Kind != KindCustomOffset || r.Status != ReminderStatusPending || r.DeadlineID != 5 {
		t.Errorf("got %+v, want kind=custom_offset status=pending deadline=5", r)
	}
	if !r.FireAt.Equal(testNow.Add(time.Hour)) {
		t.Errorf("FireAt = %v, want %v", r.FireAt, testNow.Add(time.Hour))
	}
	if r.OffsetMinutes == nil || *r.OffsetMinutes != 60 {
		t.Errorf("OffsetMinutes = %v, want 60", r.OffsetMinutes)
	}

	if _, ok := NewCustomOffsetReminder(5, -time.Hour, testNow); ok {
		t.Error("negative offset: ok = true, want false")
	}
}

func TestNewCustomAtReminder(t *testing.T) {
	fireAt := testNow.Add(-time.Hour)
	r := NewCustomAtReminder(9, fireAt)
	if r.Kind != KindCustomAt || r.Status != ReminderStatusPending || r.DeadlineID != 9 {
		t.Errorf("got %+v, want kind=custom_at status=pending deadline=9", r)
	}
	if !r.FireAt.Equal(fireAt) {
		t.Errorf("FireAt = %v, want %v", r.FireAt, fireAt)
	}
	if r.OffsetMinutes != nil {
		t.Errorf("OffsetMinutes = %v, want nil", r.OffsetMinutes)
	}
}

func TestDeadlineValidateOwnership(t *testing.T) {
	groupID := int64(1)
	ownerID := int64(2)
	cases := []struct {
		name    string
		d       Deadline
		wantErr bool
	}{
		{"group only", Deadline{GroupID: &groupID}, false},
		{"owner only", Deadline{OwnerUserID: &ownerID}, false},
		{"both set", Deadline{GroupID: &groupID, OwnerUserID: &ownerID}, true},
		{"neither set", Deadline{}, true},
	}
	for _, c := range cases {
		err := c.d.ValidateOwnership()
		if c.wantErr && err == nil {
			t.Errorf("%s: err = nil, want error", c.name)
		}
		if c.wantErr && err != nil && !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want wrapping ErrValidation", c.name, err)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s: err = %v, want nil", c.name, err)
		}
	}
}
