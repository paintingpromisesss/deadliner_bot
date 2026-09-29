// Package notifications — use cases настроек уведомлений (спека §5.2, §9
// экран 6): глобальный дефолт «дубль в ЛС» (users.dm_notify_default) и
// per-group переопределение (group_memberships.dm_notify). NULL в членстве =
// «наследовать дефолт», поэтому эффективное значение — COALESCE(membership,
// default), а не отдельная копия флага.
//
// Сервис зависит только от domain-портов (правило зависимостей), поэтому
// проверяется юнит-тестами на фейках.
package notifications

import (
	"context"
	"fmt"

	"github.com/sauron/deadliner/internal/domain"
)

// Service — настройки уведомлений пользователя.
type Service struct {
	users   domain.UserRepo
	members domain.MembershipRepo
	groups  domain.GroupRepo
}

func NewService(users domain.UserRepo, members domain.MembershipRepo, groups domain.GroupRepo) *Service {
	return &Service{users: users, members: members, groups: groups}
}

// GroupSetting — настройка одной группы: эффективное значение и его источник.
type GroupSetting struct {
	GroupID int64
	Slug    string
	Title   string
	// DMNotify — ЭФФЕКТИВНОЕ значение: COALESCE(membership.dm_notify,
	// users.dm_notify_default). Именно его применяет dm_dup fan-out (§7.3).
	DMNotify bool
	// Override — true, если у членства стоит явное значение (dm_notify IS NOT
	// NULL), то есть группа НЕ наследует глобальный дефолт.
	Override bool
}

// Settings — ответ GET /api/v1/notifications/settings.
type Settings struct {
	DMNotifyDefault bool
	Groups          []GroupSetting
}

// Get возвращает дефолт пользователя и настройки его групп. groupID == nil —
// все группы участника; иначе ровно одна (ErrNotFound, если пользователь не
// участник: 403 раскрывал бы существование чужих групп).
func (s *Service) Get(ctx context.Context, actor *domain.User, groupID *int64) (*Settings, error) {
	if actor == nil {
		return nil, fmt.Errorf("%w: anonymous actor", domain.ErrForbidden)
	}

	groups := []domain.Group{}
	if groupID != nil {
		// Членство проверяется ДО чтения группы: не участник и несуществующая
		// группа дают один и тот же NotFound.
		if _, err := s.members.Get(ctx, *groupID, actor.ID); err != nil {
			return nil, err
		}
		g, err := s.groups.GetByID(ctx, *groupID)
		if err != nil {
			return nil, err
		}
		groups = append(groups, *g)
	} else {
		list, err := s.groups.ListMine(ctx, actor.ID)
		if err != nil {
			return nil, err
		}
		groups = list
	}

	mems, err := s.members.ListByUser(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	overrides := make(map[int64]*bool, len(mems))
	for _, m := range mems {
		overrides[m.GroupID] = m.DMNotify
	}

	out := make([]GroupSetting, 0, len(groups))
	for _, g := range groups {
		ov := overrides[g.ID]
		effective := actor.DMNotifyDefault
		if ov != nil {
			effective = *ov
		}
		out = append(out, GroupSetting{
			GroupID: g.ID, Slug: g.Slug, Title: g.Title,
			DMNotify: effective, Override: ov != nil,
		})
	}
	return &Settings{DMNotifyDefault: actor.DMNotifyDefault, Groups: out}, nil
}

// UpdateInput — патч настроек. HasDMNotify отличает dm_notify:false и
// dm_notify:null от «поля нет» (для *bool это неразличимо).
type UpdateInput struct {
	// GroupID nil → глобальный дефолт; иначе переопределение для группы.
	GroupID *int64
	// DMNotify nil при HasDMNotify=true означает «снять переопределение»
	// (наследовать дефолт); при GroupID == nil недопустимо.
	DMNotify    *bool
	HasDMNotify bool
}

// Update применяет патч и возвращает актуальные настройки (клиенту нужен
// новый effective-набор, а не только код ответа).
func (s *Service) Update(ctx context.Context, actor *domain.User, in UpdateInput) (*Settings, error) {
	if actor == nil {
		return nil, fmt.Errorf("%w: anonymous actor", domain.ErrForbidden)
	}
	if !in.HasDMNotify {
		// Поле обязательно и в глобальном, и в групповом патче: запрос без
		// dm_notify ничего не меняет.
		return nil, &domain.ValidationError{Field: "dm_notify", Msg: "required"}
	}

	updated := *actor
	if in.GroupID == nil {
		// users.dm_notify_default NOT NULL — null допустим только для группы.
		if in.DMNotify == nil {
			return nil, &domain.ValidationError{
				Field: "dm_notify", Msg: "null is only allowed with group_id",
			}
		}
		// UpdateSettings пишет tz и dm_notify_default одним UPDATE: tz
		// сохраняется как есть, иначе смена флага сбрасывала бы таймзону.
		if err := s.users.UpdateSettings(ctx, actor.ID, actor.TZ, *in.DMNotify); err != nil {
			return nil, err
		}
		updated.DMNotifyDefault = *in.DMNotify
	} else {
		if _, err := s.members.Get(ctx, *in.GroupID, actor.ID); err != nil {
			return nil, err
		}
		if err := s.members.SetDMNotify(ctx, *in.GroupID, actor.ID, in.DMNotify); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, &updated, nil)
}
