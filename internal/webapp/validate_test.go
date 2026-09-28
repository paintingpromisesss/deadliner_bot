package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"testing"
	"time"
)

const testBotToken = "TEST:TOKEN"

// sign builds a valid initData query string for the given fields using the
// official Telegram WebApp HMAC scheme with testBotToken.
func sign(t *testing.T, fields url.Values) string {
	t.Helper()
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(testBotToken))

	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var check string
	for i, k := range keys {
		if i > 0 {
			check += "\n"
		}
		check += k + "=" + fields.Get(k)
	}
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(check))
	fields.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return fields.Encode()
}

func validFields(authDate time.Time) url.Values {
	return url.Values{
		"auth_date": {strconv.FormatInt(authDate.Unix(), 10)},
		"user":      {`{"id":42,"username":"ivan","first_name":"Иван","last_name":"П"}`},
		"query_id":  {"AAH"},
	}
}

func TestValidateInitData_OK(t *testing.T) {
	now := time.Now()
	raw := sign(t, validFields(now.Add(-time.Minute)))

	got, err := ValidateInitData(raw, testBotToken, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("ValidateInitData: %v", err)
	}
	if got.User.TelegramID != 42 {
		t.Errorf("TelegramID = %d, want 42", got.User.TelegramID)
	}
	if got.User.Username != "ivan" {
		t.Errorf("Username = %q, want ivan", got.User.Username)
	}
	if got.User.FirstName != "Иван" {
		t.Errorf("FirstName = %q, want Иван", got.User.FirstName)
	}
	if !got.AuthDate.Equal(now.Add(-time.Minute).Truncate(time.Second)) {
		t.Errorf("AuthDate = %v, want %v", got.AuthDate, now.Add(-time.Minute))
	}
}

func TestValidateInitData_UTF8Values(t *testing.T) {
	// Values with non-ASCII characters must survive url-encoding round trip:
	// the data_check_string uses decoded values.
	now := time.Now()
	fields := validFields(now)
	fields.Set("user", `{"id":7,"first_name":"Пётр"}`)
	raw := sign(t, fields)

	got, err := ValidateInitData(raw, testBotToken, now, time.Hour)
	if err != nil {
		t.Fatalf("ValidateInitData: %v", err)
	}
	if got.User.FirstName != "Пётр" {
		t.Errorf("FirstName = %q, want Пётр", got.User.FirstName)
	}
}

func TestValidateInitData_Tampered(t *testing.T) {
	now := time.Now()
	raw := sign(t, validFields(now))
	// Tamper: change the user payload after signing, without re-signing.
	fields, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	fields.Set("user", `{"id":99,"username":"ivan","first_name":"Иван","last_name":"П"}`)
	tampered := fields.Encode()

	if _, err := ValidateInitData(tampered, testBotToken, now, 24*time.Hour); err == nil {
		t.Fatal("tampered initData accepted, want error")
	}
}

func TestValidateInitData_OldAuthDate(t *testing.T) {
	now := time.Now()
	raw := sign(t, validFields(now.Add(-25*time.Hour)))

	if _, err := ValidateInitData(raw, testBotToken, now, 24*time.Hour); err == nil {
		t.Fatal("stale auth_date accepted, want error")
	}
}

func TestValidateInitData_MissingHash(t *testing.T) {
	fields := validFields(time.Now())
	delete(fields, "hash")
	raw := fields.Encode()

	if _, err := ValidateInitData(raw, testBotToken, time.Now(), 24*time.Hour); err == nil {
		t.Fatal("initData without hash accepted, want error")
	}
}

func TestValidateInitData_MissingUser(t *testing.T) {
	now := time.Now()
	fields := url.Values{"auth_date": {strconv.FormatInt(now.Unix(), 10)}}
	raw := sign(t, fields)

	if _, err := ValidateInitData(raw, testBotToken, now, 24*time.Hour); err == nil {
		t.Fatal("initData without user accepted, want error")
	}
}

func TestValidateInitData_UserWithoutID(t *testing.T) {
	now := time.Now()
	fields := validFields(now)
	fields.Set("user", `{"username":"noname"}`)
	raw := sign(t, fields)

	if _, err := ValidateInitData(raw, testBotToken, now, 24*time.Hour); err == nil {
		t.Fatal("user without id accepted, want error")
	}
}

func TestValidateInitData_BadAuthDate(t *testing.T) {
	now := time.Now()
	fields := validFields(now)
	fields.Set("auth_date", "not-a-number")
	raw := sign(t, fields)

	if _, err := ValidateInitData(raw, testBotToken, now, 24*time.Hour); err == nil {
		t.Fatal("non-numeric auth_date accepted, want error")
	}
}

func TestValidateInitData_EmptyRaw(t *testing.T) {
	if _, err := ValidateInitData("", testBotToken, time.Now(), time.Hour); err == nil {
		t.Fatal("empty initData accepted, want error")
	}
}
