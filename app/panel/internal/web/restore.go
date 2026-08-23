// M8.6 restore UI (M8_AUDIT.md §8: M8.5): POST /backups/restore accepts
// a multipart/form-data archive upload and hands it to the existing
// internal/backup.Restore pipeline — unpack tar.zst → strict unpack
// → manifest → integrity_check → schema compatibility → safety backup
// → pending marker, then in-process apply. The web layer only delivers
// the bytes; it never re-implements a step of the pipeline. GET
// /backups is the upload form (no separate restore page).
//
// Security invariants (M8_AUDIT.md §13 + M6 §5):
//   - the upload body is capped by MaxRestoreBodyBytes (a constant;
//     the M6 64 KiB form limit cannot fit archives) via
//     http.MaxBytesReader; an oversized body answers 413 and is never
//     fully read;
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
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/awgconf"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/db"
)

const (
	// restoreUploadPath is the restore route (also used to exempt the
	// route from the M6 body limit; the handler sets its own).
	restoreUploadPath    = "/backups/restore"
	apiRestoreUploadPath = "/api/backups/restore"
	// MaxRestoreBodyBytes caps the whole multipart upload. A backup of
	// a heavily populated panel reaches a few megabytes; 64 MiB leaves
	// a generous headroom while bounding the request (M8_AUDIT.md
	// Q13(a)). The limit is a constant so tests can assert the 413
	// behaviour against the same value the server enforces.
	MaxRestoreBodyBytes = 64 << 20 // 64 MiB
)

// Flash messages are fixed strings; they never contain request input,
// names, paths or secrets. The whole panel is Russian (T-120 round 2).
const (
	// endpointChoiceField carries the operator's answer when an archive was
	// taken on a different server: "archive" keeps the address stored in the
	// backup, "server" keeps the address of the machine being restored onto.
	endpointChoiceField = "endpoint"
	// Settings keys owned by awgconf; duplicated here rather than exported
	// so the web layer does not gain a dependency on its internals.
	settingsEndpointKey   = "endpoint"
	settingsMTUKey        = "mtu"
	endpointChoiceArchive = "archive"
	endpointChoiceServer  = "server"

	flashRestoreMissingFile     = "Не указан файл бэкапа."
	flashRestoreInvalidFileName = "Недопустимое имя файла бэкапа."
	flashRestoreFailed          = "Восстановление не удалось."
	flashRestorePending         = "Восстановление уже подготовлено. Требуется перезапуск."
	flashRestorePrepared        = "Бэкап подготовлен. Требуется перезапуск."
	// T-125: the in-process apply path. The %d is a client count, not
	// user input; both strings stay fixed.
	flashRestoreApplied     = "Восстановление применено. Активных клиентов: %d."
	flashRestoreApplyFailed = "Бэкап подготовлен, но применить его не удалось. Требуется перезапуск."
	flashRestoreNeedsChoice = "Бэкап снят на другом сервере: выберите, какой адрес использовать"
)

// pendingExists reports whether a restore is already pending next to
// the database. A probe failure is an internal error: the caller must
// not proceed, so the generic 500 page is rendered.
func (s *Server) pendingExists(w http.ResponseWriter) (bool, bool) {
	return s.pendingExistsReply(w, false)
}

func (s *Server) pendingExistsReply(w http.ResponseWriter, jsonAPI bool) (bool, bool) {
	_, ok, err := backup.PendingPath(s.cfg.DBPath)
	if err != nil {
		s.cfg.Logger.Printf("restore pending probe: %v", err)
		if jsonAPI {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "message": "Внутренняя ошибка сервера."})
		} else {
			s.errorPage(w, http.StatusInternalServerError)
		}
		return false, false
	}
	return ok, true
}

func restoreBodyExempt(path string) bool {
	return path == restoreUploadPath || path == apiRestoreUploadPath
}

func restoreForbidden(w http.ResponseWriter, jsonAPI bool) {
	if jsonAPI {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "message": "Forbidden."})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	io.WriteString(w, "Forbidden.\n")
}

func (s *Server) restoreAnswer(w http.ResponseWriter, r *http.Request, jsonAPI bool, code int, ok bool, msg string) {
	if jsonAPI {
		writeJSON(w, code, map[string]any{"ok": ok, "message": msg})
		return
	}
	s.flashBackups(w, r, msg)
}

// restoreSubmit handles POST /backups/restore. Flow:
//
//	multipart parse (own MaxBytesReader limit) → CSRF check →
//	upload saved to a private 0700 temp dir → backup.Restore → PRG.
//
// The temp upload is removed unconditionally.
func (s *Server) restoreSubmit(w http.ResponseWriter, r *http.Request) {
	s.handleRestore(w, r, false)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request, jsonAPI bool) {
	sess, _ := auth.CurrentUser(r.Context())

	// Own body limit: MaxBytesReader aborts the read (413) before the
	// upload can be processed in full.
	r.Body = http.MaxBytesReader(w, r.Body, MaxRestoreBodyBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		requestBodyError(err, w, r)
		return
	}

	tmp, err := os.MkdirTemp("", "panel-restore-*")
	if err != nil {
		internalFailure(w, r, s, "restore: create temp dir", err)
		return
	}
	removeTemp := func() { os.RemoveAll(tmp) }
	defer removeTemp()
	uploadPath := filepath.Join(tmp, "upload.tar.zst")

	var (
		csrfToken      string
		uploaded       bool
		fileName       string
		endpointChoice string
	)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			requestBodyError(err, w, r)
			return
		}
		switch part.FormName() {
		case auth.CSRFFieldName:
			csrfToken = readPartText(part)
		case endpointChoiceField:
			endpointChoice = readPartText(part)
		case "backup":
			fileName = part.FileName()
			if fileName == "" {
				break // a field-shaped part, not a file
			}
			if err := saveUpload(part, uploadPath); err != nil {
				requestBodyError(err, w, r)
				return
			}
			uploaded = true
		default:
			// unknown parts are skipped but still drained so the body
			// is consumed within the limit
		}
		part.Close()
	}

	headerToken := r.Header.Get(auth.CSRFHeaderName)
	if !auth.CSRFValid(sess.CSRFToken, csrfToken) && !auth.CSRFValid(sess.CSRFToken, headerToken) {
		restoreForbidden(w, jsonAPI)
		return
	}

	if !uploaded {
		s.restoreAnswer(w, r, jsonAPI, http.StatusBadRequest, false, flashRestoreMissingFile)
		return
	}
	if !validUploadName(fileName) {
		s.restoreAnswer(w, r, jsonAPI, http.StatusBadRequest, false, flashRestoreInvalidFileName)
		return
	}

	pending, ok := s.pendingExistsReply(w, jsonAPI)
	if !ok {
		return
	}
	if pending {
		code := http.StatusSeeOther
		if jsonAPI {
			code = http.StatusConflict
		}
		s.restoreAnswer(w, r, jsonAPI, code, false, flashRestoreBlockedByPending)
		return
	}

	// An archive from another server carries that server's endpoint (the
	// address baked into every client config) and the MTU measured for its
	// uplink. Applying those silently is what breaks a migration, so ask
	// before touching anything — and only when they actually differ.
	archived, err := backup.Inspect(uploadPath)
	if err != nil {
		s.cfg.Logger.Printf("restore inspect: %v", err)
		s.restoreAnswer(w, r, jsonAPI, http.StatusBadRequest, false, flashRestoreFailed)
		return
	}
	liveEndpoint := s.settingOrEmpty(settingsEndpointKey)
	liveMTU := s.settingOrEmpty(settingsMTUKey)
	differs := archived.Endpoint != liveEndpoint || archived.MTU != liveMTU
	if differs && endpointChoice == "" {
		if jsonAPI {
			writeJSON(w, http.StatusConflict, map[string]any{
				"ok":               false,
				"needs_choice":     true,
				"message":          flashRestoreNeedsChoice,
				"archive_endpoint": archived.Endpoint,
				"server_endpoint":  liveEndpoint,
				"archive_mtu":      archived.MTU,
				"server_mtu":       liveMTU,
			})
			return
		}
		s.restoreAnswer(w, r, jsonAPI, http.StatusSeeOther, false, flashRestoreNeedsChoice)
		return
	}

	s.mutex.Lock()
	_, err = backup.Restore(s.db(), s.cfg.DBPath, uploadPath, backupsDir(), nil)
	var appliedN int
	var applyErr error
	if err == nil {
		appliedN, applyErr = s.applyRestoreNow(sess.Username)
	}
	s.mutex.Unlock()
	if errors.Is(err, backup.ErrRestorePending) {
		code := http.StatusSeeOther
		if jsonAPI {
			code = http.StatusConflict
		}
		s.restoreAnswer(w, r, jsonAPI, code, false, flashRestorePending)
		return
	}
	if err != nil {
		s.cfg.Logger.Printf("restore: %v", err)
		s.restoreAnswer(w, r, jsonAPI, http.StatusBadRequest, false, flashRestoreFailed)
		return
	}
	if applyErr != nil {
		s.cfg.Logger.Printf("restore apply: %v", applyErr)
		s.restoreAnswer(w, r, jsonAPI, http.StatusBadRequest, false, flashRestoreApplyFailed)
		return
	}
	// "server": the restored database carries the old host's address and
	// MTU; put this machine's values back so clients reach the server they
	// were just migrated to.
	if endpointChoice == endpointChoiceServer {
		if err := s.restoreHostSettings(liveEndpoint, liveMTU); err != nil {
			s.cfg.Logger.Printf("restore host settings: %v", err)
		}
	}
	if _, err := db.AuthUserByUsername(s.db(), sess.Username); err != nil {
		auth.ClearSessionCookie(w)
	}
	s.restoreAnswer(w, r, jsonAPI, http.StatusOK, true, fmt.Sprintf(flashRestoreApplied, appliedN))
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
// still-live handle and a restart can retry. Sessions are wiped after
// a successful apply (T-138) unless keepUsername still exists in the
// preserved live auth (T-155).
func (s *Server) applyRestoreNow(keepUsername string) (int, error) {
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
	if err := backup.KeepLiveAuth(s.cfg.DBPath); err != nil {
		next.Close()
		return fail(fmt.Errorf("keep live auth: %w", err))
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
	if _, err := db.AuthUserByUsername(next, keepUsername); err != nil {
		s.cfg.Sessions.DeleteAll()
	}
	clients, err := db.ClientsAll(next)
	if err != nil {
		s.cfg.Logger.Printf("restore apply: client count: %v", err)
		return 0, nil
	}
	return len(clients), nil
}

// readPartText drains a small form-field part into a string.
func readPartText(part *multipart.Part) string {
	b, err := io.ReadAll(io.LimitReader(part, 4<<10))
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

// validUploadName refuses traversal-shaped file names and anything that
// is not a panel backup archive (.tar.zst). The upload is always
// written to a private temp path (the name is never used as a path),
// but a name carrying path separators or ".." segments is rejected up
// front (M8.6 requirement).
func validUploadName(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return false
	}
	if filepath.Base(name) != name {
		return false
	}
	return strings.HasSuffix(strings.ToLower(name), ".tar.zst")
}

// settingOrEmpty reads one settings key, treating any failure as "unset":
// the caller only compares it against the archive.
func (s *Server) settingOrEmpty(key string) string {
	v, _, err := db.GetSetting(s.db(), key)
	if err != nil {
		return ""
	}
	return v
}

// restoreHostSettings writes this machine's endpoint and MTU back over the
// values the archive brought with it. An empty value is skipped rather than
// stored: it would leave `client config` unable to render an endpoint.
func (s *Server) restoreHostSettings(endpoint, mtu string) error {
	for key, value := range map[string]string{
		settingsEndpointKey: endpoint,
		settingsMTUKey:      mtu,
	} {
		if value == "" {
			continue
		}
		if err := db.SetSetting(s.db(), key, value); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	return nil
}
