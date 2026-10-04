package slugprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sauron/deadliner/internal/domain"
)

// --- fake GroupRepo (только SearchByPrefix реально используется Suggest'ом) ---

type fakeGroupRepo struct {
	groups  []domain.Group
	lastPfx string
	lastID  int64
	lastLim int
	calls   int
	err     error
}

func (r *fakeGroupRepo) SearchByPrefix(ctx context.Context, prefix string, callerID int64, limit int) ([]domain.Group, error) {
	r.calls++
	r.lastPfx, r.lastID, r.lastLim = prefix, callerID, limit
	if r.err != nil {
		return nil, r.err
	}
	out := []domain.Group{}
	for _, g := range r.groups {
		if strings.HasPrefix(g.SlugNorm, prefix) {
			out = append(out, g)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (r *fakeGroupRepo) Create(ctx context.Context, g *domain.Group) error { return nil }
func (r *fakeGroupRepo) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeGroupRepo) GetBySlugNorm(ctx context.Context, slugNorm string) (*domain.Group, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeGroupRepo) Update(ctx context.Context, g *domain.Group) error { return nil }
func (r *fakeGroupRepo) SetStatus(ctx context.Context, id int64, status domain.GroupStatus) error {
	return nil
}
func (r *fakeGroupRepo) SoftDelete(ctx context.Context, id int64) error { return nil }
func (r *fakeGroupRepo) HardDelete(ctx context.Context, id int64) error { return nil }
func (r *fakeGroupRepo) ListMine(ctx context.Context, userID int64) ([]domain.Group, error) {
	return nil, nil
}
func (r *fakeGroupRepo) ListPendingExpired(ctx context.Context, now time.Time, limit int) ([]domain.Group, error) {
	return nil, nil
}

// ListAll — часть domain.GroupRepo, нужная только CLI `admin list-groups`;
// сервисам этих пакетов не требуется.
func (r *fakeGroupRepo) ListAll(ctx context.Context, status *domain.GroupStatus, limit int) ([]domain.Group, error) {
	return nil, nil
}

// --- Validate: charset из SLUG_REGEX, правила Deadliner поверх ---------------

// Дефолтная регулярка: вузовский формат принимается.
func TestValidateAcceptsDefaultFormat(t *testing.T) {
	p, err := NewLocal(domain.DefaultSlugRegex, &fakeGroupRepo{})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	for _, slug := range []string{"М8О-401Б-23", "ИКБО-33-21", "AB-1"} {
		if err := p.Validate(slug); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", slug, err)
		}
	}
}

// Набор символов берётся ИЗ КОНФИГА: символ вне дефолтной регулярки
// принимается, если оператор расширил SLUG_REGEX.
func TestValidateUsesConfiguredCharset(t *testing.T) {
	const slug = "ЁЖ9" // «Ё» вне дефолтного А-Я и не нормализуется в Ж

	deflt, err := NewLocal(domain.DefaultSlugRegex, &fakeGroupRepo{})
	if err != nil {
		t.Fatalf("NewLocal(default): %v", err)
	}
	if err := deflt.Validate(slug); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Errorf("Validate(%q) with the default regex = %v, want ErrInvalidSlug", slug, err)
	}

	loose, err := NewLocal(`^[А-ЯЁA-Z0-9]+(-[А-ЯЁA-Z0-9]+)*$`, &fakeGroupRepo{})
	if err != nil {
		t.Fatalf("NewLocal(loose): %v", err)
	}
	if err := loose.Validate(slug); err != nil {
		t.Errorf("Validate(%q) with an Ё-enabled SLUG_REGEX = %v, want nil", slug, err)
	}
}

// Обратная сторона: оператор СУЗИЛ регулярку — слаг вузовского формата
// отклоняется, несмотря на цифры в нём.
func TestValidateRejectsWhenConfiguredRegexIsStricter(t *testing.T) {
	p, err := NewLocal(`^[A-Z]+$`, &fakeGroupRepo{})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if err := p.Validate("М8О-401Б-23"); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Errorf("Validate = %v, want ErrInvalidSlug (regex charset from config)", err)
	}
	if err := p.Validate("GROUP"); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Errorf("Validate(GROUP) = %v, want ErrInvalidSlug (digit rule layers on top)", err)
	}
}

// Анти-спам-правило Deadliner (минимум одна цифра) действует при ЛЮБОЙ
// регулярке: ослабивший charset оператор не отключает его заодно.
func TestValidateDigitRuleSurvivesAnyRegex(t *testing.T) {
	// Регулярка намеренно безцифровая и допускает пустые сегменты (двойной
	// дефис): обе проверки должны прийти из домена, а не из неё.
	p, err := NewLocal(`^[А-ЯA-Z-]+$`, &fakeGroupRepo{})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if err := p.Validate("БЕЗЦИФР"); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Errorf("Validate(БЕЗЦИФР) = %v, want ErrInvalidSlug (digit rule is Deadliner's, not the regex's)", err)
	}
	if err := p.Validate("АБ--В"); !errors.Is(err, domain.ErrInvalidSlug) {
		t.Errorf("Validate(АБ--В) = %v, want ErrInvalidSlug (empty segment rule)", err)
	}
}

// Ошибки валидации оборачивают доменный сентинел: HTTP-слой маппит их в 400
// по errors.Is(err, domain.ErrInvalidSlug).
func TestValidateWrapsDomainSentinel(t *testing.T) {
	p, err := NewLocal(`^[0-9]+$`, &fakeGroupRepo{})
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	err = p.Validate("АБВ")
	if err == nil {
		t.Fatal("Validate(АБВ) = nil, want error")
	}
	if !errors.Is(err, domain.ErrInvalidSlug) {
		t.Errorf("Validate = %v, want wrapping domain.ErrInvalidSlug", err)
	}
}

// --- NewLocal / Compile -----------------------------------------------------

// Некомпилируемый SLUG_REGEX — ошибка конструктора (config.Load отвергает то
// же значение на старте, см. internal/config).
func TestInvalidRegexIsAnError(t *testing.T) {
	if _, err := NewLocal(`^[А-Я`, &fakeGroupRepo{}); err == nil {
		t.Error("NewLocal(invalid regex) = nil error, want failure")
	}
	if _, err := Compile(`(`); err == nil {
		t.Error("Compile(invalid) = nil error, want failure")
	}
	if _, err := Compile(domain.DefaultSlugRegex); err != nil {
		t.Errorf("Compile(default) = %v, want nil", err)
	}
}

// --- Suggest ----------------------------------------------------------------

// Suggest — обёртка над SearchByPrefix: префикс нормализуется, callerID и
// limit доезжают до репозитория как есть.
func TestSuggestDelegatesToSearchByPrefix(t *testing.T) {
	repo := &fakeGroupRepo{groups: []domain.Group{
		{ID: 1, Slug: "ИКБО-33-21", SlugNorm: "ИКБО-33-21", Status: domain.GroupStatusActive},
		{ID: 2, Slug: "М8О-401Б-23", SlugNorm: "М8О-401Б-23", Status: domain.GroupStatusActive},
	}}
	p, err := NewLocal(domain.DefaultSlugRegex, repo)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	got, err := p.Suggest(context.Background(), " икбо ", 7, 20)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("Suggest(икбо) = %+v, want the single ИКБО group", got)
	}
	if repo.lastPfx != "ИКБО" {
		t.Errorf("prefix passed to repo = %q, want normalized ИКБО", repo.lastPfx)
	}
	if repo.lastID != 7 {
		t.Errorf("callerID passed to repo = %d, want 7", repo.lastID)
	}
	if repo.lastLim != 20 {
		t.Errorf("limit passed to repo = %d, want 20", repo.lastLim)
	}
}

// Ошибка репозитория проходит насквозь: сервис групп отдаёт её в HTTP-слой.
func TestSuggestPropagatesRepoError(t *testing.T) {
	want := fmt.Errorf("db down")
	repo := &fakeGroupRepo{err: want}
	p, err := NewLocal(domain.DefaultSlugRegex, repo)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if _, err := p.Suggest(context.Background(), "И", 1, 5); !errors.Is(err, want) {
		t.Errorf("Suggest err = %v, want the repo error", err)
	}
}
