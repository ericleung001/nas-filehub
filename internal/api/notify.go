package api

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/mail"
)

const notifyMinInterval = 30 * time.Second

func (s *Server) sendMail(cfg db.SMTPSettings, to, subject, body string) error {
	fn := s.MailSend
	if fn == nil {
		fn = mail.Send
	}
	return fn(cfg, to, subject, body)
}

func (s *Server) notifyAllowed(to, subject string) bool {
	key := strings.ToLower(strings.TrimSpace(to)) + "\x00" + subject
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.notifyLast == nil {
		s.notifyLast = make(map[string]time.Time)
	}
	now := time.Now()
	if prev, ok := s.notifyLast[key]; ok && now.Sub(prev) < notifyMinInterval {
		return false
	}
	s.notifyLast[key] = now
	return true
}

func (s *Server) notifyEmail(to, subject, body string) {
	to = strings.TrimSpace(to)
	if to == "" {
		return
	}
	run := func() { s.notifyEmailSync(to, subject, body) }
	if s.NotifySync {
		run()
		return
	}
	go run()
}

func (s *Server) notifyEmailSync(to, subject, body string) {
	cfg, err := s.effectiveSMTP()
	if err != nil || !cfg.Ready() {
		return
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return
	}
	if !s.notifyAllowed(to, subject) {
		return
	}
	if err := s.sendMail(cfg, to, subject, body); err != nil {
		log.Printf("smtp: could not send %q to %s: %v", subject, to, err)
	}
}

func (s *Server) notifyPendingRegistration(username, email string) {
	users, err := s.DB.ListUsers()
	if err != nil {
		log.Printf("smtp: could not list admins for registration notice: %v", err)
		return
	}
	subject := "Filehub: new account pending approval"
	body := fmt.Sprintf(
		"A new Filehub account is waiting for admin approval.\n\nUsername: %s\nEmail: %s\n\nSign in to Filehub and open Settings → Users to approve or reject it.\n",
		username, strings.TrimSpace(email),
	)
	for _, u := range users {
		if !u.IsAdmin || u.Disabled {
			continue
		}
		if strings.TrimSpace(u.Email) == "" {
			continue
		}
		s.notifyEmail(u.Email, subject, body)
	}
}

func (s *Server) notifyShareGranted(userID, shareID int64, perm string, canDelete bool) {
	u, err := s.DB.GetUserByID(userID)
	if err != nil || u == nil || strings.TrimSpace(u.Email) == "" {
		return
	}
	sh, err := s.DB.GetShare(shareID)
	if err != nil || sh == nil {
		return
	}
	access := "read"
	if strings.EqualFold(perm, "write") {
		access = "write"
	}
	deleteNote := "Delete is not allowed."
	if canDelete {
		deleteNote = "Delete is allowed."
	}
	subject := "Filehub: folder access granted"
	body := fmt.Sprintf(
		"You have been granted access to the shared folder %q.\n\nAccess: %s\n%s\n\nSign in to Filehub to open it.\n",
		sh.Name, access, deleteNote,
	)
	s.notifyEmail(u.Email, subject, body)
}
