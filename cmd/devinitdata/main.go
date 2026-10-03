// devinitdata — генератор валидного initData для локальной разработки TMA.
//
// Подписывает query-строку тем же алгоритмом, что проверяет сервер
// (internal/webapp.ValidateInitData: HMAC-SHA256 с ключом от BOT_TOKEN), поэтому
// сгенерированная строка проходит аутентификацию на локальном сервере.
//
// Использование:
//
//	go run ./cmd/devinitdata -bot-token "$BOT_TOKEN" [-id 42] [-username durov] [-name Pavel]
//
// Вывод — URL с launch params: откройте его в браузере, и фронтовый SDK
// определит Mini App-окружение по query-параметрам, как это делает Telegram.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	botToken := flag.String("bot-token", os.Getenv("BOT_TOKEN"), "токен бота (или переменная BOT_TOKEN)")
	baseURL := flag.String("url", "http://localhost:5173/", "база URL, к которой прикрепить launch params")
	userID := flag.Int64("id", 42, "telegram_id пользователя")
	username := flag.String("username", "dev", "username пользователя")
	firstName := flag.String("name", "Dev", "first_name пользователя")
	flag.Parse()

	if *botToken == "" {
		fmt.Fprintln(os.Stderr, "bot-token обязателен: -bot-token или переменная BOT_TOKEN")
		os.Exit(2)
	}

	// Поле user — JSON, как его присылает Telegram.
	userJSON := fmt.Sprintf(`{"id":%d,"username":%q,"first_name":%q}`, *userID, *username, *firstName)
	// auth_date — сейчас: сервер проверяет свежесть (AUTH_DATE_MAX_AGE_HOURS).
	authDate := strconv.FormatInt(time.Now().Unix(), 10)

	values := url.Values{}
	values.Set("auth_date", authDate)
	values.Set("query_id", "devquery")
	// signature — обязательный ключ схемы SDK (проверяется его наличие, не
	// значение; серверную подпись несёт hash). Пустая строка проходит.
	values.Set("signature", "")
	values.Set("user", userJSON)

	// data_check_string: пары без hash, отсортированные по ключу, "key=value"
	// через "\n" (зеркало алгоритма из internal/webapp/validate.go).
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	checkString := make([]string, 0, len(keys))
	for _, k := range keys {
		checkString = append(checkString, k+"="+values.Get(k))
	}

	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(*botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(checkString, "\n")))
	hash := hex.EncodeToString(mac.Sum(nil))
	values.Set("hash", hash)

	// SDK читает launch params из query: tgWebAppData (initData) и окружение.
	// tgWebAppVersion и tgWebAppThemeParams обязательны схемой LaunchParams.
	launch := url.Values{}
	launch.Set("tgWebAppData", values.Encode())
	launch.Set("tgWebAppPlatform", "tdesktop")
	launch.Set("tgWebAppVersion", "7.0")
	launch.Set("tgWebAppThemeParams", `{"bg_color":"#1c1c1d","text_color":"#f5f5f5","hint_color":"#7e7e80"}`)

	sep := "?"
	if strings.HasSuffix(*baseURL, "?") {
		sep = ""
	}
	fmt.Println(*baseURL + sep + launch.Encode())
}
