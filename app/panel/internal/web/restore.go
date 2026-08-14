// M8.6 restore UI (M8_AUDIT.md §8: M8.5): the panel gains a
// multipart/form-data upload page (GET /backups/restore, POST
// /backups/restore) that hands the archive to the existing
// internal/backup.Restore pipeline — upload → decrypt → strict unpack
// → manifest → integrity_check → schema compatibility → safety backup
// → pending marker. The web layer only delivers the bytes and the
// identity; it never re-implements a step of the pipeline.
//
// Security invariants (M8_AUDIT.md §13 + M6 §5):
//   - the upload body is capped by MaxRestoreBodyBytes (a constant;
//     the M6 64 KiB form limit cannot fit archives) via
//     http.MaxBytesReader; an oversized body answers 413 and is never
//     fully read;
//   - the age identity is accepted only in memory: it is parsed from
//     the raw part bytes, every temporary copy is zeroed, it is never
//     written to disk and never logged or echoed into HTML, redirects
//     or flashes;
//   - every failure is generic: fixed flash messages or the generic
//     error page — no temp file names, paths, SQL, manifest contents
//     or archive bytes reach the client; real errors are logged
//     server-side;
//   - the CSRF token is validated here (not by RequireCSRF, which
//     cannot parse multipart bodies): the same primitives,
//     auth.CSRFFieldName + auth.CSRFValid, answer the same fixed 403;
//   - restore never restarts the panel and never touches Docker or
//     awg0.conf; it stops at the pending marker ("restart required").
package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const (
	// restoreUploadPath is the restore route (also used to exempt the
	// route from the M6 body limit; the handler sets its own).
	restoreUploadPath = "/backups/restore"
	// MaxRestoreBodyBytes caps the whole multipart upload. A backup of
	// a heavily populated panel reaches a few megabytes; 64 MiB leaves
	// a generous headroom while bounding the request (M8_AUDIT.md
	// Q13(a)). The limit is a constant so tests can assert the 413
	// behaviour against the same value the server enforces.
	MaxRestoreBodyBytes = 64 << 20 // 64 MiB
	// restoreIdentityLimit caps the identity field: a handful of
	// AGE-SECRET-KEY lines, DoS-safe.
	restoreIdentityLimit = 64 << 10
)

// Flash messages are fixed strings; they never contain request input,
// identity material, names, paths or secrets. The whole panel is
// Russian (T-120 round 2).
const (
	flashRestoreMissingIdentity = "Не указан ключ (identity)."
	flashRestoreInvalidIdentity = "Недопустимый ключ (identity)."
	flashRestoreMissingFile     = "Не указан файл бэкапа."
	flashRestoreInvalidFileName = "Недопустимое имя файла бэкапа."
	flashRestoreFailed          = "Восстановление не удалось."
	flashRestorePending         = "Восстановление уже подготовлено. Требуется перезапуск."
	flashRestorePrepared        = "Бэкап подготовлен. Требуется перезапуск."
	// T-125: the in-process apply path. The %d is a client count, not
	// user input; both strings stay fixed.
	flashRestoreApplied     = "Восстановление применено. Активных клиентов: %d."
	flashRestoreApplyFailed = "Бэкап подготовлен, но применить его не удалось. Требуется перезапуск."
)

// restoreData is the GET /backups/restore payload. It carries no
// secret field; identity is supplied through the upload form only.
type restoreData struct {
	Flash    string
	Username string
	CSRF     string
	// Pending reflects an existing .restore-pending marker: the page
	// then refuses further restores and explains that the panel never
	// restarts itself.
	Pending bool
}

// pendingExists reports whether a restore is already pending next to
// the database. A probe failure is an internal error: the caller must
// not proceed, so the generic 500 page is rendered.
func (s *Server) pendingExists(w http.ResponseWriter) (bool, bool) {
	_, ok, err := backup.PendingPath(s.cfg.DBPath)
	if err != nil {
		s.cfg.Logger.Printf("restore pending probe: %v", err)
		s.errorPage(w, http.StatusInternalServerError)
		return false, false
	}
	return ok, true
}

// restorePage handles GET /backups/restore: the upload form. It shows
// the pending banner when a restore is already prepared and never
// restarts the panel.
func (s *Server) restorePage(w http.ResponseWriter, r *http.Request) {
	pending, ok := s.pendingExists(w)
	if !ok {
		return
	}
	sess, _ := auth.CurrentUser(r.Context())
	s.renderRestore(w, restoreData{
		Flash:    r.URL.Query().Get("msg"),
		Username: sess.Username,
		CSRF:     sess.CSRFToken,
		Pending:  pending,
	})
}

// renderRestore executes the standalone "restore" template (like
// "login"/"error"/"backups": it must not define the layout blocks or
// it would hijack the dashboard's title/body).
func (s *Server) renderRestore(w http.ResponseWriter, data restoreData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := s.tpl.ExecuteTemplate(w, "restore", data); err != nil {
		s.cfg.Logger.Printf("render restore: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// restoreSubmit handles POST /backups/restore. Flow:
//
//	multipart parse (own MaxBytesReader limit) → CSRF check →
//	identity (memory only) → upload saved to a private 0700 temp dir →
//	backup.Restore → PRG.
//
// The temp upload is encrypted (.age) by construction; it is still
// removed unconditionally. The identity never touches the file system.
func (s *Server) restoreSubmit(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.CurrentUser(r.Context())

	// Own body limit: MaxBytesReader aborts the read (413) before the
	// upload can be processed in full.
	r.Body = http.MaxBytesReader(w, r.Body, MaxRestoreBodyBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		requestBodyError(err, w)
		return
	}

	tmp, err := os.MkdirTemp("", "panel-restore-*")
	if err != nil {
		internalFailure(w, s, "restore: create temp dir", err)
		return
	}
	removeTemp := func() { os.RemoveAll(tmp) }
	defer removeTemp()
	uploadPath := filepath.Join(tmp, "upload.age")

	var (
		csrfToken string
		identity  []byte
		uploaded  bool
		fileName  string
	)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			requestBodyError(err, w)
			return
		}
		switch part.FormName() {
		case auth.CSRFFieldName:
			csrfToken = readPartText(part)
		case "identity":
			b, err := io.ReadAll(io.LimitReader(part, restoreIdentityLimit+1))
			if err != nil {
				requestBodyError(err, w)
				return
			}
			if len(b) > restoreIdentityLimit {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				http.Error(w, "Request body too large.", http.StatusRequestEntityTooLarge)
				return
			}
			identity = b
		case "backup":
			fileName = part.FileName()
			if fileName == "" {
				break // a field-shaped part, not a file
			}
			if err := saveUpload(part, uploadPath); err != nil {
				requestBodyError(err, w)
				return
			}
			uploaded = true
		default:
			// unknown parts are skipped but still drained so the body
			// is consumed within the limit
		}
		part.Close()
	}

	// CSRF: the same fixed 403 as RequireCSRF, token accepted only as
	// a form part.
	if !auth.CSRFValid(sess.CSRFToken, csrfToken) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "Forbidden.\n")
		return
	}

	ids, msg := parseIdentityLines(identity)
	if msg != "" {
		s.flashBackups(w, r, msg)
		return
	}
	if !uploaded {
		s.flashBackups(w, r, flashRestoreMissingFile)
		return
	}
	if !validUploadName(fileName) {
		s.flashBackups(w, r, flashRestoreInvalidFileName)
		return
	}

	pending, ok := s.pendingExists(w)
	if !ok {
		return
	}
	if pending {
		s.flashBackups(w, r, flashRestoreBlockedByPending)
		return
	}
	recipient, err := backup.RecipientFromEnv()
	if err != nil {
		s.cfg.Logger.Printf("restore: recipient: %v", err)
		s.flashBackups(w, r, flashBackupUnconfigured)
		return
	}

	// The pipeline (M8.4 core) performs decrypt → strict unpack →
	// manifest → integrity_check → schema compatibility → safety
	// backup → pending. The live database is never modified before the
	// safety backup exists; the marker is exclusive (a second restore
	// answers ErrRestorePending). On success the restore is applied
	// in-process (T-125) — the panel restarts nothing.
	s.mutex.Lock()
	_, err = backup.Restore(s.db(), s.cfg.DBPath, uploadPath, backupsDir(), recipient, ids, nil)
	var appliedN int
	var applyErr error
	if err == nil {
		appliedN, applyErr = s.applyRestoreNow()
	}
	s.mutex.Unlock()
	clear(identity)
	if errors.Is(err, backup.ErrRestorePending) {
		s.flashBackups(w, r, flashRestorePending)
		return
	}
	if err != nil {
		s.cfg.Logger.Printf("restore: %v", err)
		s.flashBackups(w, r, flashRestoreFailed)
		return
	}
	if applyErr != nil {
		s.cfg.Logger.Printf("restore apply: %v", applyErr)
		s.flashBackups(w, r, flashRestoreApplyFailed)
		return
	}
	auth.ClearSessionCookie(w)
	s.flashBackups(w, r, fmt.Sprintf(flashRestoreApplied, appliedN))
}

// testFault, when set by tests, injects the named failure into the
// in-process apply path after ApplyPending (nil in production).
var testFault func(step string) error

func fault(step string) error {
	if testFault == nil {
		return nil
	}
	return testFault(step)
}

// applyRestoreNow applies a prepared restore in-process (T-125):
// backup.ApplyPending swaps the database file, a fresh handle is
// opened and migrated, and awg0.conf is regenerated so the awg runtime
// activates the restored peers on its next hot reload (syncconf).
// Callers must hold s.mutex.
//
// Open, Migrate and Generate must all succeed before the apply is
// considered done (T-141). On any of those failures the file swap is
// reverted and the pending marker is restored so disk matches the
// still-live handle and a restart can retry. Sessions are wiped only
// after a successful apply (T-138).
func (s *Server) applyRestoreNow() (int, error) {
	applied, err := backup.ApplyPending(s.cfg.DBPath)
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, errors.New("no pending restore to apply")
	}
	fail := func(err error) (int, error) {
		if rerr := backup.RevertApply(s.cfg.DBPath); rerr != nil {
			return 0, fmt.Errorf("%w (also revert apply: %v)", err, rerr)
		}
		return 0, err
	}

	if err := fault("restore.apply.open"); err != nil {
		return fail(fmt.Errorf("open restored database: %w", err))
	}
	next, err := db.Open(s.cfg.DBPath)
	if err != nil {
		return fail(fmt.Errorf("open restored database: %w", err))
	}
	if err := fault("restore.apply.migrate"); err != nil {
		next.Close()
		return fail(fmt.Errorf("migrate restored database: %w", err))
	}
	if err := db.Migrate(next); err != nil {
		next.Close()
		return fail(fmt.Errorf("migrate restored database: %w", err))
	}
	if err := fault("restore.apply.generate"); err != nil {
		next.Close()
		return fail(fmt.Errorf("regenerate config: %w", err))
	}
	if err := awgconf.Generate(next, s.cfg.ConfPath); err != nil {
		next.Close()
		return fail(fmt.Errorf("regenerate config: %w", err))
	}
	old := s.swapDB(next)
	retireDB(old)
	s.cfg.Sessions.DeleteAll()
	s.pendingTOTP = make(map[string]string)
	clients, err := db.ClientsAll(next)
	if err != nil {
		s.cfg.Logger.Printf("restore apply: client count: %v", err)
		return 0, nil
	}
	return len(clients), nil
}

// readPartText drains a small form-field part into a string.
func readPartText(part *multipart.Part) string {
	b, err := io.ReadAll(io.LimitReader(part, restoreIdentityLimit+1))
	if err != nil {
		return ""
	}
	return string(b)
}

// saveUpload streams one file part into dst (0600, O_EXCL). The
// MaxBytesReader on the body limits the copy; a partial copy is
// removed by the caller's RemoveAll.
func saveUpload(part *multipart.Part, dst string) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, cErr := io.Copy(f, part)
	closeErr := f.Close()
	if cErr != nil {
		return cErr
	}
	return closeErr
}

// validUploadName refuses traversal-shaped file names: the upload is
// always written to a private temp path (the name is never used as a
// path), but a name carrying path separators or ".." segments is
// rejected up front (M8.6 requirement).
func validUploadName(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return false
	}
	return filepath.Base(name) == name
}

// parseIdentityLines converts the raw identity part into age
// identities. Every string copy derived from the raw bytes is zeroed
// before returning; the parsed age.Identity values live in memory by
// design (the restore needs them) and are garbage-collected like any
// other secret. Errors are generic: the offending line is never
// echoed.
func parseIdentityLines(raw []byte) ([]age.Identity, string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, flashRestoreMissingIdentity
	}
	var ids []age.Identity
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		copyLine := append([]byte(nil), line...)
		s := string(copyLine)
		id, err := age.ParseX25519Identity(s)
		clear(copyLine)
		if err != nil {
			return nil, flashRestoreInvalidIdentity
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, flashRestoreMissingIdentity
	}
	return ids, ""
}
