package ethol

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"sync"
	"time"
)

const (
	classReminderLead  = 15 * time.Minute
	taskReminderWindow = 24 * time.Hour
	reminderRetention  = 48 * time.Hour
	reminderInterval   = 5 * time.Minute
)

// ReminderEngine sends proactive class-start and task-deadline notifications.
type ReminderEngine struct {
	academic *AcademicManager
	courses  *CourseManager
	auth     *AuthManager
	notifier *TelegramNotifier
	mu       sync.Mutex
	sent     map[string]time.Time
	now      func() time.Time
}

func NewReminderEngine(auth *AuthManager, courses *CourseManager, academic *AcademicManager, notifier *TelegramNotifier) *ReminderEngine {
	return &ReminderEngine{
		academic: academic,
		courses:  courses,
		auth:     auth,
		notifier: notifier,
		sent:     make(map[string]time.Time),
		now:      NowWIB,
	}
}

func (re *ReminderEngine) Run(ctx context.Context, interval time.Duration) {
	if re.notifier == nil || re.academic == nil || re.courses == nil {
		return
	}
	if interval <= 0 {
		interval = reminderInterval
	}
	re.CheckOnce(ctx)
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			re.CheckOnce(ctx)
		}
	}
}

// CheckOnce sends any due reminders and returns how many were sent. Sent keys
// are deduplicated in memory; a restart may resend at most one reminder.
func (re *ReminderEngine) CheckOnce(ctx context.Context) int {
	if re.academic == nil || re.courses == nil {
		return 0
	}
	if re.auth != nil {
		if err := re.auth.EnsureSession(ctx); err != nil {
			slog.Warn("Reminder check skipped: session unavailable", "error", err)
			return 0
		}
	}

	tahun, semester, err := re.courses.ActivePeriod(ctx)
	if err != nil {
		slog.Warn("Reminder check skipped: active period unavailable", "error", err)
		return 0
	}
	courses, err := re.courses.GetCourses(ctx)
	if err != nil {
		slog.Warn("Reminder check skipped: courses unavailable", "error", err)
		return 0
	}

	now := re.now()
	sent := re.checkClassReminders(ctx, now, tahun, semester, courses)
	sent += re.checkTaskReminders(ctx, now, courses)
	re.cleanup(now)
	return sent
}

func (re *ReminderEngine) checkClassReminders(ctx context.Context, now time.Time, tahun, semester int, courses []Course) int {
	items, err := re.academic.GetSchedule(ctx, tahun, semester)
	if err != nil {
		slog.Warn("Reminder check skipped: schedule unavailable", "error", err)
		return 0
	}

	nowWIB := now.In(WIBLocation)
	dayVal := int(nowWIB.Weekday())
	if dayVal == 0 {
		dayVal = 7
	}

	sent := 0
	for _, item := range items {
		if item.DayValue() != dayVal {
			continue
		}
		start, err := parseClockToTime(item.JamAwal, nowWIB)
		if err != nil {
			continue
		}
		until := start.Sub(nowWIB)
		if until <= 0 || until > classReminderLead {
			continue
		}

		name := item.CourseName()
		if c := matchCourse(item, courses); c != nil {
			name = c.CourseName()
		}
		key := fmt.Sprintf("class:%s:%s:%s", TodayDate(nowWIB), item.JamAwal, name)
		if !re.markSent(key, now) {
			continue
		}

		msg := fmt.Sprintf("⏰ <b>KELAS SEBENTAR LAGI</b>\n\n📚 <b>%s</b>\n🕒 Mulai %s WIB\n⏳ %s lagi",
			html.EscapeString(name), html.EscapeString(item.JamAwal), humanizeDuration(until))
		if item.Ruang != "" {
			msg += fmt.Sprintf("\n📍 %s", html.EscapeString(item.Ruang))
		}
		if item.Dosen != "" {
			msg += fmt.Sprintf("\n👨‍🏫 %s", html.EscapeString(item.Dosen))
		}
		if err := re.notifier.SendMessage(ctx, msg); err != nil {
			slog.Warn("Failed to send class reminder", "error", err)
			continue
		}
		sent++
	}
	return sent
}

func (re *ReminderEngine) checkTaskReminders(ctx context.Context, now time.Time, courses []Course) int {
	tasks, err := re.academic.GetPendingTasks(ctx, courses)
	if err != nil {
		slog.Warn("Reminder check skipped: tasks unavailable", "error", err)
		return 0
	}

	sent := 0
	for _, task := range tasks {
		deadline, ok := parseTaskDeadline(task.Deadline)
		if !ok {
			continue
		}
		until := deadline.Sub(now)
		if until <= 0 || until > taskReminderWindow {
			continue
		}

		key := fmt.Sprintf("task:%d:%s:%s", task.KuliahID, task.Title, task.Deadline)
		if !re.markSent(key, now) {
			continue
		}

		msg := fmt.Sprintf("📝 <b>DEADLINE TUGAS</b>\n\n<b>%s</b>\n📚 %s\n⏰ %s\n⏳ Sisa %s",
			html.EscapeString(task.Title), html.EscapeString(task.CourseName),
			html.EscapeString(task.Deadline), humanizeDuration(until))
		if err := re.notifier.SendMessage(ctx, msg); err != nil {
			slog.Warn("Failed to send task reminder", "error", err)
			continue
		}
		sent++
	}
	return sent
}

func (re *ReminderEngine) markSent(key string, now time.Time) bool {
	re.mu.Lock()
	defer re.mu.Unlock()
	if _, ok := re.sent[key]; ok {
		return false
	}
	re.sent[key] = now
	return true
}

func (re *ReminderEngine) cleanup(now time.Time) {
	re.mu.Lock()
	defer re.mu.Unlock()
	for k, t := range re.sent {
		if now.Sub(t) > reminderRetention {
			delete(re.sent, k)
		}
	}
}

func humanizeDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d menit", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%d jam", h)
	}
	return fmt.Sprintf("%d jam %d menit", h, m)
}
