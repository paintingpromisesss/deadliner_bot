// Package i18n — каталог строк интерфейса (ru.json). Загружается один раз
// на старте через Load(Locales); в коде используются константные ключи и T().
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
)

//go:embed locales
var Locales embed.FS

var (
	mu      sync.RWMutex
	catalog = map[string]string{}
)

// Load читает все *.json из каталогов "locales" внутри fsys и заменяет
// ими каталог в памяти. Вызывается один раз на старте.
func Load(fsys embed.FS) error {
	merged := map[string]string{}
	found := false
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".json" || path.Base(path.Dir(p)) != "locales" {
			return nil
		}
		data, err := fsys.ReadFile(p)
		if err != nil {
			return err
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("i18n: %s: %w", p, err)
		}
		for k, v := range m {
			merged[k] = v
		}
		found = true
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("i18n: no locales/*.json found in embed.FS")
	}
	mu.Lock()
	catalog = merged
	mu.Unlock()
	return nil
}

// MustLoad — Load с паникой на ошибке (для старта приложения).
func MustLoad(fsys embed.FS) {
	if err := Load(fsys); err != nil {
		panic(err)
	}
}

// Plural — русская форма числительного: «1 день», «2 дня», «5 дней»,
// «21 день» (n%100 в 11..14 → родительный множественный). Общий хелпер:
// используется и напоминаниями (scheduler), и claim-флоу.
func Plural(n int, one, few, many string) string {
	nAbs := n
	if nAbs < 0 {
		nAbs = -nAbs
	}
	switch {
	case nAbs%100 >= 11 && nAbs%100 <= 14:
		return many
	case nAbs%10 == 1:
		return one
	case nAbs%10 >= 2 && nAbs%10 <= 4:
		return few
	default:
		return many
	}
}

// T возвращает строку каталога по ключу; отсутствующий ключ — сам ключ.
// При непустых args значение трактуется как форматная строка fmt.
func T(key string, args ...any) string {
	mu.RLock()
	v, ok := catalog[key]
	mu.RUnlock()
	if !ok {
		return key
	}
	if len(args) == 0 {
		return v
	}
	return fmt.Sprintf(v, args...)
}

// EscapeHTML экранирует пользовательский текст под parse_mode=HTML
// (подмножество Telegram: &, <, >). Единый хелпер для всех подстановок
// данных пользователя: без экранирования Telegram либо отвергнет сообщение
// («can't parse entities»), либо исполнит чужую разметку.
func EscapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// Plain убирает разметку каталога из готовой строки (значения размечены под
// parse_mode=HTML, спека §6.2, но часть сообщений уходит без разметки).
// Поддерживаемое подмножество: <b> <i> <u> <s> <code> <pre> <a href="…">.
// Пользовательские значения должны быть прогнаны через EscapeHTML — иначе
// Plain вырежет и их «теги».
func Plain(s string) string {
	for _, tag := range []string{"b", "i", "u", "s", "code", "pre"} {
		s = strings.ReplaceAll(s, "<"+tag+">", "")
		s = strings.ReplaceAll(s, "</"+tag+">", "")
	}
	// <a href="...">текст</a> → текст.
	for {
		start := strings.Index(s, "<a href=")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], ">")
		if end < 0 {
			break
		}
		s = s[:start] + s[start+end+1:]
	}
	return strings.ReplaceAll(s, "</a>", "")
}
