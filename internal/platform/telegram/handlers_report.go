package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot/models"

	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
)

// handleReportSlug — /report_slug <slug> (спека §3.3, «Жалобы»): админ группы
// жалуется на конфликтующий слаг, и жалоба уходит в ЛС всем супер-админам.
//
// Только ЛС: в общем чате команда молча игнорируется. Это тот же приём, что у
// служебных команд — ответ в чате раскрыл бы участникам и существование
// команды, и сам факт конфликта слагов.
//
// Ответ вызывающему ОБОБЩЁННЫЙ и одинаковый при успехе, отсутствии прав и
// неизвестном слаге: иначе перебором текста можно было бы выяснить, какие
// слаги заняты. Права и существование группы проверяет use case
// (groups.Service.ReportSlug); сбой доставки супер-админам тоже не
// раскрывается — рассылка best-effort (§7.3), факт попытки остаётся в аудите.
//
// Забаненный вызывающий отсекается как в /bind_group: гидратация нужна потому,
// что actor из touchUser частичный (is_banned не читается), а команда живёт
// только в боте и под middleware.Auth не попадает.
func (h *Handlers) handleReportSlug(ctx context.Context, msg *models.Message, actor *domain.User, arg string) {
	if !isPrivate(msg.Chat.Type) {
		return
	}
	if actor == nil || h.reports == nil {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.error.generic"), false)
		return
	}
	if banned, err := h.isBanned(ctx, actor.TelegramID); err != nil {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.error.generic"), false)
		return
	} else if banned {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.forbidden"), false)
		return
	}

	slug := strings.TrimSpace(arg)
	if slug == "" {
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.report.usage"), false)
		return
	}

	_, err := h.reports.ReportSlug(ctx, actor, slug)
	switch {
	case err == nil, errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrForbidden):
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.report.sent"), false)
	default:
		h.log.Warn("telegram: /report_slug failed", slog.String("error", err.Error()))
		h.send(ctx, msg.Chat.ID, nil, i18n.T("bot.error.generic"), false)
	}
}
