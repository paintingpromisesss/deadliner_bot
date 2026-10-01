// Package cmd — подрежимы единого бинарника `deadliner` (спека §2):
// migrate и admin. serve собирается в Task 16.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sauron/deadliner/internal/app/moderation"
	"github.com/sauron/deadliner/internal/config"
	"github.com/sauron/deadliner/internal/domain"
	"github.com/sauron/deadliner/internal/i18n"
	"github.com/sauron/deadliner/internal/platform/db"
)

// Коды выхода CLI (спека §2: подрежим admin): 0 — успех, 1 — ошибка операции,
// 2 — ошибка использования (неверная команда/аргументы).
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// Admin — подрежим `deadliner admin <команда> [аргумент]`. Команды:
//
//	promote <telegram_id>    выдать права супер-админа (синоним promote-superadmin)
//	ban <telegram_id>        забанить (сессии отзываются немедленно)
//	unban <telegram_id>      снять бан
//	delete-group <slug>      soft-delete группы по слагу
//	list-groups [status]     таблица групп: слаг, название, статус, участники,
//	                         создана (status: pending|active|archived)
//	stats                    счётчики инстанса
//	cleanup                  один прогон cleanup (протухшие pending-группы,
//	                         счётчики, сессии)
//
// Миграции НЕ применяются: CLI работает с уже существующей схемой (миграции —
// отдельный подрежим migrate), иначе случайный `admin stats` на проде менял бы
// схему. Ошибка → код 1, ошибка использования → 2.
func Admin(ctx context.Context, args []string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	code := run(ctx, args, log, os.Stdout, os.Stderr)
	if code == exitOK {
		return nil
	}
	// Ненулевой код — детали уже напечатаны пользователю; main выставит код.
	return errExitCode(code)
}

// errExitCode — сигнал main'у, каким кодом завершиться. Детали вывода CLI
// печатает сам (человекочитаемый текст, а не ошибка Go-стека).
type errExitCode int

func (e errExitCode) Error() string { return fmt.Sprintf("admin: exit code %d", int(e)) }

// ExitCode — код выхода для main (0, если ошибка не от CLI).
func ExitCode(err error) int {
	var code errExitCode
	if errors.As(err, &code) {
		return int(code)
	}
	if err != nil {
		return exitError
	}
	return exitOK
}

// run — тестируемая сердцевина Admin: вся работа идёт в out/errOut, коды
// выхода возвращаются значением (никаких os.Exit внутри).
func run(ctx context.Context, args []string, log *slog.Logger, out, errOut io.Writer) int {
	// Каталог строк нужен и справке, и текстам действий: ошибка загрузки — не
	// повод падать (ключи вернутся как есть).
	if err := i18n.Load(i18n.Locales); err != nil {
		fmt.Fprintf(errOut, "warning: i18n catalog: %v\n", err)
	}

	cmd, arg, ok := parseArgs(args)
	if !ok {
		usage(errOut)
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(errOut, "config: %v\n", err)
		return exitError
	}
	pool, err := db.Connect(ctx, cfg.DB.URL, int32(cfg.DB.PoolMax))
	if err != nil {
		fmt.Fprintf(errOut, "db: %v\n", err)
		return exitError
	}
	defer pool.Close()

	svc := newModerationService(newRepos(pool), cfg, domain.SystemClock{}, log)
	return dispatch(ctx, svc, cmd, arg, out, errOut)
}

// parseArgs разбирает `admin <команда> [аргумент]`. ok=false — ошибка
// использования (неизвестная команда, лишний/отсутствующий аргумент,
// нечисловой telegram_id).
func parseArgs(args []string) (cmd, arg string, ok bool) {
	if len(args) == 0 {
		return "", "", false
	}
	cmd = strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]

	switch cmd {
	case "promote", "promote-superadmin", "ban", "unban":
		if len(rest) != 1 {
			return canonical(cmd), "", false
		}
		id, err := strconv.ParseInt(strings.TrimSpace(rest[0]), 10, 64)
		if err != nil || id == 0 {
			return canonical(cmd), "", false
		}
		return canonical(cmd), strconv.FormatInt(id, 10), true
	case "delete-group":
		if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
			return cmd, "", false
		}
		return cmd, strings.TrimSpace(rest[0]), true
	case "list-groups":
		// Аргумент необязателен: без него показываются все неудалённые группы.
		if len(rest) > 1 {
			return cmd, "", false
		}
		if len(rest) == 1 {
			return cmd, strings.ToLower(strings.TrimSpace(rest[0])), true
		}
		return cmd, "", true
	case "stats", "cleanup":
		if len(rest) != 0 {
			return cmd, "", false
		}
		return cmd, "", true
	default:
		return cmd, "", false
	}
}

// canonical — brief-спека §2 называет команду promote-superadmin, спека §6.1 —
// promote: принимаем обе, дальше работаем с канонической короткой формой.
func canonical(cmd string) string {
	if cmd == "promote-superadmin" {
		return "promote"
	}
	return cmd
}

// usage печатает справку: справка по неверному вводу — это stderr.
func usage(w io.Writer) {
	fmt.Fprintln(w, i18n.T("admin.usage"))
}

// dispatch выполняет разобранную команду. Актор — SystemActor: команда
// запускается оператором на сервере, строки users у неё нет, поэтому аудит
// пишется с actor_user_id = NULL (см. moderation.writeAudit).
func dispatch(ctx context.Context, svc *moderation.Service, cmd, arg string, out, errOut io.Writer) int {
	actor := moderation.SystemActor

	switch cmd {
	case "promote":
		tgID, _ := strconv.ParseInt(arg, 10, 64)
		if err := svc.PromoteSuperadmin(ctx, actor, tgID); err != nil {
			return fail(errOut, "promote", err)
		}
		fmt.Fprintln(out, i18n.T("admin.promoted", arg))

	case "ban":
		tgID, _ := strconv.ParseInt(arg, 10, 64)
		if err := svc.BanUser(ctx, actor, tgID); err != nil {
			return fail(errOut, "ban", err)
		}
		fmt.Fprintln(out, i18n.T("admin.banned", arg))

	case "unban":
		tgID, _ := strconv.ParseInt(arg, 10, 64)
		if err := svc.UnbanUser(ctx, actor, tgID); err != nil {
			return fail(errOut, "unban", err)
		}
		fmt.Fprintln(out, i18n.T("admin.unbanned", arg))

	case "delete-group":
		g, err := svc.DeleteGroup(ctx, actor, arg)
		if err != nil {
			return fail(errOut, "delete-group", err)
		}
		fmt.Fprintln(out, i18n.T("admin.group_deleted", g.Slug, g.Title))

	case "stats":
		st, err := svc.Stats(ctx, actor)
		if err != nil {
			return fail(errOut, "stats", err)
		}
		printStats(out, st)

	case "list-groups":
		status, ok := parseGroupStatus(arg)
		if !ok {
			fmt.Fprintln(errOut, i18n.T("admin.list_groups.bad_status", arg))
			return exitUsage
		}
		summaries, truncated, err := svc.ListGroups(ctx, actor, status, 0)
		if err != nil {
			return fail(errOut, "list-groups", err)
		}
		printGroups(out, summaries, truncated)

	case "cleanup":
		report, err := svc.CleanupExpiredPending(ctx)
		if err != nil {
			return fail(errOut, "cleanup", err)
		}
		fmt.Fprintln(out, i18n.T("admin.cleanup.done", svc.PendingTTL()))
		printReport(out, report)
	}
	return exitOK
}

// printStats — человекочитаемая сводка (выравнивание по самой длинной метке).
func printStats(w io.Writer, st domain.Stats) {
	rows := [][2]string{
		{i18n.T("admin.stats.users"), formatInt(st.Users)},
		{i18n.T("admin.stats.groups"), formatInt(st.GroupsTotal)},
		{i18n.T("admin.stats.groups_active"), formatInt(st.GroupsActive)},
		{i18n.T("admin.stats.groups_pending"), formatInt(st.GroupsPending)},
		{i18n.T("admin.stats.deadlines_active"), formatInt(st.DeadlinesActive)},
		{i18n.T("admin.stats.reminders_pending"), formatInt(st.RemindersPending)},
		{i18n.T("admin.stats.reminders_failed"), formatInt(st.RemindersFailed)},
		{i18n.T("admin.stats.sessions_active"), formatInt(st.SessionsActive)},
	}
	width := 0
	for _, r := range rows {
		if n := len([]rune(r[0])); n > width {
			width = n
		}
	}
	for _, r := range rows {
		pad := strings.Repeat(" ", width-len([]rune(r[0])))
		fmt.Fprintln(w, i18n.T("admin.stats.row", r[0]+pad, r[1]))
	}
}

// printReport — отчёт cleanup-прогона.
func printReport(w io.Writer, r moderation.CleanupReport) {
	fmt.Fprintln(w, i18n.T("admin.cleanup.groups", formatInt(int64(r.Groups))))
	fmt.Fprintln(w, i18n.T("admin.cleanup.reminders", formatInt(int64(r.Reminders))))
	fmt.Fprintln(w, i18n.T("admin.cleanup.counters", formatInt(int64(r.CountersPurged))))
	fmt.Fprintln(w, i18n.T("admin.cleanup.sessions", formatInt(int64(r.SessionsPurged))))
}

// parseGroupStatus — необязательный фильтр `admin list-groups [status]`.
// Пустой аргумент → nil (все статусы); мусор → ok=false (ошибка использования,
// выход 2). Значения совпадают с группами статусов в domain.GroupStatus.
func parseGroupStatus(arg string) (*domain.GroupStatus, bool) {
	if arg == "" {
		return nil, true
	}
	st := domain.GroupStatus(arg)
	switch st {
	case domain.GroupStatusPending, domain.GroupStatusActive, domain.GroupStatusArchived:
		return &st, true
	default:
		return nil, false
	}
}

// printGroups — таблица `admin list-groups`. Колонки выравниваются по самой
// длинной ячейке: слаг — ASCII/кириллица, а title произвольный, поэтому
// фиксированная ширина ломала бы вывод (см. printStats с той же логикой).
//
// Число колонок фиксировано пятью (спека §2), независимо от содержимого:
// выравнивание по максимальной ширине ВНУТРИ колонки, а не по всей строке.
func printGroups(w io.Writer, rows []moderation.GroupSummary, truncated bool) {
	if len(rows) == 0 {
		fmt.Fprintln(w, i18n.T("admin.list_groups.empty"))
		return
	}

	header := []string{
		i18n.T("admin.list_groups.slug"),
		i18n.T("admin.list_groups.title"),
		i18n.T("admin.list_groups.status"),
		i18n.T("admin.list_groups.members"),
		i18n.T("admin.list_groups.created"),
	}
	table := make([][]string, 0, len(rows)+1)
	table = append(table, header)
	for _, r := range rows {
		table = append(table, []string{
			r.Slug,
			r.Title,
			string(r.Status),
			formatInt(int64(r.MembersCount)),
			r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	widths := make([]int, len(header))
	for _, row := range table {
		for i, cell := range row {
			if n := len([]rune(cell)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for _, row := range table {
		var b strings.Builder
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(cell)
			// Последняя колонка не добивается пробелами (хвостовые пробелы в
			// терминале — мусор, по которому ломается копипаста).
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))))
			}
		}
		fmt.Fprintln(w, b.String())
	}

	if truncated {
		fmt.Fprintln(w, i18n.T("admin.list_groups.truncated", formatInt(int64(moderation.ListGroupsLimit))))
	}
}

// fail печатает ошибку операции человекочитаемо и возвращает код 1.
func fail(w io.Writer, cmd string, err error) int {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		fmt.Fprintln(w, i18n.T("admin.error.not_found", cmd))
	case errors.Is(err, domain.ErrForbidden):
		fmt.Fprintln(w, i18n.T("admin.error.forbidden", cmd, err.Error()))
	default:
		fmt.Fprintln(w, i18n.T("admin.error.generic", cmd, err.Error()))
	}
	return exitError
}

// formatInt — число для текста каталога.
func formatInt(n int64) string { return strconv.FormatInt(n, 10) }
