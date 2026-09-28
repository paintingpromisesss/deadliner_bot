package domain

import "time"

type ReminderStatus string

const (
	ReminderStatusPending   ReminderStatus = "pending"
	ReminderStatusSent      ReminderStatus = "sent"
	ReminderStatusFailed    ReminderStatus = "failed"
	ReminderStatusCancelled ReminderStatus = "cancelled"
)

type ReminderKind string

const (
	KindPreset       ReminderKind = "preset"
	KindCustomOffset ReminderKind = "custom_offset"
	KindCustomAt     ReminderKind = "custom_at"
	KindDMDup        ReminderKind = "dm_dup"
)

type Reminder struct {
	ID         int64
	DeadlineID int64
	Kind       ReminderKind
	// TargetUserID — цель dm_dup-ребёнка (fan-out §7.3): users.id получателя
	// дубля в ЛС. NULL для обычных reminder-строк.
	TargetUserID  *int64
	OffsetMinutes *int
	FireAt        time.Time
	Status        ReminderStatus
	Attempts      int
	LockedBy      *string
	LockedAt      *time.Time
	LastError     string
	SentAt        *time.Time
}

// PlanReminders builds preset reminders: fire_at = due_at - offset; offsets
// whose fire_at is not strictly in the future are dropped; duplicate offsets
// are collapsed.
func PlanReminders(d Deadline, presets []time.Duration, now time.Time) []Reminder {
	seen := make(map[time.Duration]bool, len(presets))
	out := make([]Reminder, 0, len(presets))
	for _, off := range presets {
		if seen[off] {
			continue
		}
		seen[off] = true
		fireAt := d.DueAt.Add(-off)
		if !fireAt.After(now) {
			continue
		}
		mins := int(off / time.Minute)
		out = append(out, Reminder{
			DeadlineID:    d.ID,
			Kind:          KindPreset,
			OffsetMinutes: &mins,
			FireAt:        fireAt,
			Status:        ReminderStatusPending,
		})
	}
	return out
}

func NewCustomOffsetReminder(deadlineID int64, offset time.Duration, now time.Time) (Reminder, bool) {
	fireAt := now.Add(offset)
	if fireAt.Before(now) {
		return Reminder{}, false
	}
	mins := int(offset / time.Minute)
	return Reminder{
		DeadlineID:    deadlineID,
		Kind:          KindCustomOffset,
		OffsetMinutes: &mins,
		FireAt:        fireAt,
		Status:        ReminderStatusPending,
	}, true
}

func NewCustomAtReminder(deadlineID int64, fireAt time.Time) Reminder {
	return Reminder{
		DeadlineID: deadlineID,
		Kind:       KindCustomAt,
		FireAt:     fireAt,
		Status:     ReminderStatusPending,
	}
}
