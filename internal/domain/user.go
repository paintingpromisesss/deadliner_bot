package domain

import "time"

type User struct {
	ID              int64
	TelegramID      int64
	Username        string
	FirstName       string
	TZ              string
	DMNotifyDefault bool
	IsSuperadmin    bool
	IsBanned        bool
	BotBlocked      bool
	CreatedAt       time.Time
}
