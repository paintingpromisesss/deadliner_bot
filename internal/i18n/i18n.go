// Package i18n — каталог строк интерфейса (ru.json). Загружается один раз
// на старте через Load(Locales); в коде используются константные ключи и T().
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
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
