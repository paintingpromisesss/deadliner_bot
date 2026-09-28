package domain

import "time"

// SystemClock — реализация Clock на системных часах (UTC).
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
