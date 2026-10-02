// Package slugprovider — локальная реализация domain.SlugProvider (спека
// §2.2): точка расширения под будущий API МАИ, сегодня работающая на
// SLUG_REGEX из конфига и локальной таблице groups.
//
// Разделение ответственности: SLUG_REGEX (env) отвечает за НАБОР СИМВОЛОВ и
// форму слага, доменный domain.ValidateStrict — за собственные правила
// Deadliner (длина 3..16, непустые сегменты, минимум одна цифра). При
// замене local-провайдера на «API МАИ» второе правило действует ровно так
// же: это добавление продукта, а не часть вузовского формата.
package slugprovider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/sauron/deadliner/internal/domain"
)

// Local — domain.SlugProvider на локальных данных: SLUG_REGEX + таблица групп.
type Local struct {
	re     *regexp.Regexp
	groups domain.GroupRepo
}

var _ domain.SlugProvider = (*Local)(nil)

// NewLocal компилирует SLUG_REGEX и собирает провайдер. Некомпилируемое
// выражение — ошибка конструктора; config.Load проверяет то же самое на
// старте, поэтому ошибка здесь означала бы расхождение путей конфигурации,
// а не пользовательский ввод.
func NewLocal(expr string, groups domain.GroupRepo) (*Local, error) {
	re, err := Compile(expr)
	if err != nil {
		return nil, err
	}
	return &Local{re: re, groups: groups}, nil
}

// Compile — единственная точка компиляции SLUG_REGEX: config.Load и
// local-провайдер обязаны принимать ровно одни выражения.
func Compile(expr string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("slugprovider: SLUG_REGEX is not a valid regular expression: %w", err)
	}
	return re, nil
}

// Validate проверяет НОРМАЛИЗОВАННЫЙ слаг: сначала SLUG_REGEX (набор
// символов и форма), затем правила Deadliner (domain.ValidateStrict — длина
// 3..16 и обязательная цифра).
//
// Цифра остаётся обязательной при ЛЮБОМ SLUG_REGEX: это анти-спам-правило
// продукта (спека §3.3), а не часть вузовского формата, поэтому оператор,
// ослабивший регулярку, не отключает его заодно.
func (l *Local) Validate(slug string) error {
	if !l.re.MatchString(slug) {
		return fmt.Errorf("%w: does not match SLUG_REGEX", domain.ErrInvalidSlug)
	}
	return domain.ValidateStrict(slug)
}

// Suggest — подсказки слага по префиксу: обёртка над
// GroupRepo.SearchByPrefix. callerID отделяет активные группы (видны всем)
// от pending (видны только создателю, спека §6.4).
func (l *Local) Suggest(ctx context.Context, prefix string, callerID int64, limit int) ([]domain.Group, error) {
	return l.groups.SearchByPrefix(ctx, domain.Normalize(prefix), callerID, limit)
}
