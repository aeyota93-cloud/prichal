package main

import (
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Telegram settings from the panel: connect, change, check, switch off. The
// token only travels from the browser to here and on to Telegram; no answer,
// log line or timeline entry contains it.

var (
	tgUserRe  = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)
	tgTokenRe = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{30,}$`)
)

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func (s *server) telegramStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.tg.Status())
}

func (s *server) telegramSave(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Username string }
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	user := cleanUsername(in.Username)
	if !tgUserRe.MatchString(user) {
		badRequest(w, "Ник в Telegram: 5–32 латинских буквы, цифры или _")
		return
	}
	token := strings.Join(strings.Fields(in.Token), "")
	if token == "" {
		if token = s.tg.token(); token == "" {
			badRequest(w, "Вставьте токен бота")
			return
		}
	} else if !tgTokenRe.MatchString(token) {
		badRequest(w, "Это не похоже на токен: он выглядит как 123456789:AA…")
		return
	}

	ctx, cancel := ctxTimeout(r, 10*time.Second)
	defer cancel()
	bot, err := s.tg.Check(ctx, token)
	if err != nil {
		var te *tgError
		if errors.As(err, &te) && (te.Status == http.StatusUnauthorized || te.Status == http.StatusNotFound) {
			badRequest(w, "Telegram не принял токен: скопируйте его из @BotFather целиком")
			return
		}
		// The error is already redacted; the second pass is a belt and braces.
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "Не удалось связаться с Telegram: " + shortErr(errors.New(strings.ReplaceAll(err.Error(), token, "<token>"))),
		})
		return
	}

	changed := s.tg.Status().State != "off"
	if err := s.tg.Apply(TgConfig{Token: token, Username: user, Bot: bot}); err != nil {
		log.Printf("telegram: не удалось сохранить настройки: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сохранить настройки"})
		return
	}
	if changed {
		s.journal.Add("", "Уведомления в Telegram изменены: @"+user)
	} else {
		s.journal.Add("", "Уведомления в Telegram подключены: @"+user)
	}
	writeJSON(w, http.StatusOK, s.tg.Status())
}

func (s *server) telegramTest(w http.ResponseWriter, r *http.Request) {
	if s.tg.Status().State != "on" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Бот ещё не привязан: откройте его в Telegram и нажмите Start"})
		return
	}
	ctx, cancel := ctxTimeout(r, 15*time.Second)
	defer cancel()
	if err := s.tg.SendNow(ctx, "Проверка связи: Причал на месте ✓"); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Не получилось отправить: " + shortErr(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (s *server) telegramOff(w http.ResponseWriter, r *http.Request) {
	if err := s.tg.Disable(); err != nil {
		log.Printf("telegram: не удалось сохранить настройки: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сохранить настройки"})
		return
	}
	s.journal.Add("", "Уведомления в Telegram отключены")
	writeJSON(w, http.StatusOK, s.tg.Status())
}
