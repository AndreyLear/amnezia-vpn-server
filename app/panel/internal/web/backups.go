// M8.5 backup management UI (M8_AUDIT.md §8: M8.4): the panel gets a
// /backups page — list, create (POST /backups/create), download (GET
// /backups/{name}/download) and delete (POST /backups/{name}/delete).
// Every route runs behind RequireAuth; the two POSTs additionally run
// behind RequireCSRF (auth → csrf → handler, same chain as M6.3
// mutations).
//
// Security invariants (same as M6_AUDIT.md §5):
//   - a backup name is accepted only through the strict M8.3 name
//     validation (backup-YYYY-MM-DD.tar.zst.age); bare names are joined
//     to the backups directory, so absolute paths and ".." cannot
//     resolve anywhere else; only regular files (Lstat) are listed,
//     downloaded or deleted — symlinks are refused;
//   - the page renders no secrets: AGE_RECIPIENT never appears, flash
//     messages are fixed strings that never echo user input, and
//     internal errors are logged server-side and answered with fixed
//     flash messages or the generic error page;
//   - download answers attachment-only bytes (Content-Disposition),
//     never HTML.
package web

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/auth"
	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
)

// Flash messages are fixed strings; they never contain request input,
// names, paths or secrets. The whole panel is Russian (T-120 round 2).
const (
	flashBackupCreated      = "Бэкап создан."
	flashBackupDeleted      = "Бэкап удалён."
	flashBackupNotFound     = "Бэкап не найден."
	flashBackupInvalidName  = "Недопустимое имя бэкапа."
	flashBackupCreateFailed = "Не удалось создать бэкап."
	flashBackupDeleteFailed = "Не удалось удалить бэкап."
	flashBackupUnconfigured = "Шифрование бэкапов не настроено."
	// flashRestoreBlockedByPending is also shown when create/delete is
	// attempted while a restore is pending (M8.6: no mutation while a
	// restore is prepared).
	flashRestoreBlockedByPending = "Восстановление уже подготовлено. Требуется перезапуск."
	// T-125 one-click download.
	flashBackupDownloadFailed = "Не удалось скачать бэкап."
)

// backupsDir mirrors cli.backupsPath(): the deployment configures the
// location through AMNEZIA_BACKUPS_PATH; the default is /data/backups
// (the M8.7 compose mount ./backups:/data/backups, RW only panel).
func backupsDir() string {
	if p := os.Getenv("AMNEZIA_BACKUPS_PATH"); p != "" {
		return p
	}
	return "/data/backups"
}

// backupNameRe and validBackupName mirror cli (M8.3): the archive
// naming contract of spec §5, including the embedded date being a real
// calendar date. Only bare names can pass; "/", ".." and everything
// else never match.
var backupNameRe = regexp.MustCompile(`^backup-(\d{4}-\d{2}-\d{2})\.tar\.zst\.age$`)

func validBackupName(name string) bool {
	m := backupNameRe.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	_, err := time.Parse("2006-01-02", m[1])
	return err == nil
}

// backupsData is the GET /backups payload. It carries no secret field:
// only names, display dates and sizes of validated archive files.
type backupsData struct {
	Flash    string
	Username string
	CSRF     string
	Backups  []backupRow
	// Pending reflects an existing .restore-pending marker: the page
	// shows the restart-required banner and the restore link.
	Pending bool
}

// backupRow is one validated archive in the listing.
type backupRow struct {
	Name     string
	DateText string
	SizeText string
}

// flashBackups completes the PRG cycle towards /backups with a fixed
// message.
func (s *Server) flashBackups(w http.ResponseWriter, r *http.Request, msg string) {
	redirect303(w, r, "/backups?msg="+url.QueryEscape(msg))
}

// backupsPage handles GET /backups: it lists only files that pass the
// strict name validation AND are regular files (Lstat — a symlink or a
// directory with a matching name is never shown). A missing backups
// directory is an empty list, like the CLI.
func (s *Server) backupsPage(w http.ResponseWriter, r *http.Request) {
	dir := backupsDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.cfg.Logger.Printf("backups page: %v", err)
		s.errorPage(w, http.StatusInternalServerError)
		return
	}
	var rows []backupRow
	for _, e := range entries {
		name := e.Name()
		if !validBackupName(name) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		rows = append(rows, backupRow{
			Name:     name,
			DateText: fi.ModTime().UTC().Format("2006-01-02 15:04:05 UTC"),
			SizeText: bytesText(uint64(fi.Size())),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	pending, ok := s.pendingExists(w)
	if !ok {
		return
	}
	sess, _ := auth.CurrentUser(r.Context())
	s.renderBackups(w, backupsData{
		Flash:    r.URL.Query().Get("msg"),
		Username: sess.Username,
		CSRF:     sess.CSRFToken,
		Backups:  rows,
		Pending:  pending,
	})
}

// renderBackups executes the standalone "backups" template (like
// "login"/"error": it must not define the layout blocks or it would
// hijack the dashboard's title/body).
func (s *Server) renderBackups(w http.ResponseWriter, data backupsData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := s.tpl.ExecuteTemplate(w, "backups", data); err != nil {
		s.cfg.Logger.Printf("render backups: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// backupCreate handles POST /backups/create: it calls the M8.2
// pipeline (backup.Create) under the server mutex and PRG-redirects to
// /backups. Failures are logged server-side and answered with fixed
// flash messages — the staging path and the error text never reach the
// client. backup.Create cleans its own staging on success and failure.
func (s *Server) backupCreate(w http.ResponseWriter, r *http.Request) {
	if pending, ok := s.pendingExists(w); !ok {
		return
	} else if pending {
		s.flashBackups(w, r, flashRestoreBlockedByPending)
		return
	}
	recipient, err := backup.RecipientFromEnv()
	if err != nil {
		s.cfg.Logger.Printf("backup create: recipient: %v", err)
		s.flashBackups(w, r, flashBackupUnconfigured)
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, err := backup.Create(s.db(), backupsDir(), recipient, time.Now); err != nil {
		s.cfg.Logger.Printf("backup create: %v", err)
		s.flashBackups(w, r, flashBackupCreateFailed)
		return
	}
	s.flashBackups(w, r, flashBackupCreated)
}

// backupDownloadNow handles POST /backups/download (T-125): a fresh
// encrypted archive of the live database is created in a private temp
// directory, streamed as an attachment and removed — nothing is stored
// in the backups listing. CSRF-protected; paused while a restore is
// pending; requires AGE_RECIPIENT like backup create.
func (s *Server) backupDownloadNow(w http.ResponseWriter, r *http.Request) {
	if pending, ok := s.pendingExists(w); !ok {
		return
	} else if pending {
		s.flashBackups(w, r, flashRestoreBlockedByPending)
		return
	}
	recipient, err := backup.RecipientFromEnv()
	if err != nil {
		s.cfg.Logger.Printf("backup download: recipient: %v", err)
		s.flashBackups(w, r, flashBackupUnconfigured)
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	tmp, err := os.MkdirTemp("", "panel-download-*")
	if err != nil {
		s.cfg.Logger.Printf("backup download: temp dir: %v", err)
		s.flashBackups(w, r, flashBackupDownloadFailed)
		return
	}
	defer os.RemoveAll(tmp)
	archive, err := backup.Create(s.db(), tmp, recipient, time.Now)
	if err != nil {
		s.cfg.Logger.Printf("backup download: create: %v", err)
		s.flashBackups(w, r, flashBackupDownloadFailed)
		return
	}
	f, err := os.Open(archive)
	if err != nil {
		s.cfg.Logger.Printf("backup download: open: %v", err)
		s.flashBackups(w, r, flashBackupDownloadFailed)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		s.cfg.Logger.Printf("backup download: stat: %v", err)
		s.flashBackups(w, r, flashBackupDownloadFailed)
		return
	}
	// backup.Create returns a bare archive name (backup-YYYY-MM-DD.tar.zst.age).
	name := filepath.Base(archive)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	if _, err := io.Copy(w, f); err != nil {
		s.cfg.Logger.Printf("backup download: stream: %v", err)
	}
}

// backupDownload handles GET /backups/{name}/download: the name must
// pass the strict validation (it is then a bare name inside the
// backups directory), the file must be a regular file (Lstat), and the
// response is the exact archive bytes with Content-Disposition
// attachment. Any deviation answers the generic 404 — nothing about
// the requested name is echoed.
func (s *Server) backupDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validBackupName(name) {
		s.errorPage(w, http.StatusNotFound)
		return
	}
	full := filepath.Join(backupsDir(), name)
	st, err := os.Lstat(full)
	if err != nil || !st.Mode().IsRegular() {
		s.errorPage(w, http.StatusNotFound)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		s.errorPage(w, http.StatusNotFound)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		s.errorPage(w, http.StatusNotFound)
		return
	}
	// The validated name contains only [a-z0-9.-]: it is safe inside a
	// quoted header value (no quotes, no CR/LF).
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	if _, err := io.Copy(w, f); err != nil {
		s.cfg.Logger.Printf("backup download: %v", err)
	}
}

// backupDelete handles POST /backups/{name}/delete: the strict name
// validation plus Lstat + os.Remove guarantee that only a regular file
// inside the backups directory can be unlinked (os.Remove never
// follows symlinks). The flow is PRG with fixed flash messages; the
// name is never echoed.
func (s *Server) backupDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validBackupName(name) {
		s.flashBackups(w, r, flashBackupInvalidName)
		return
	}
	if pending, ok := s.pendingExists(w); !ok {
		return
	} else if pending {
		s.flashBackups(w, r, flashRestoreBlockedByPending)
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	full := filepath.Join(backupsDir(), name)
	st, err := os.Lstat(full)
	if err != nil || !st.Mode().IsRegular() {
		s.flashBackups(w, r, flashBackupNotFound)
		return
	}
	if err := os.Remove(full); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.flashBackups(w, r, flashBackupNotFound)
			return
		}
		s.cfg.Logger.Printf("backup delete: %v", err)
		s.flashBackups(w, r, flashBackupDeleteFailed)
		return
	}
	s.flashBackups(w, r, flashBackupDeleted)
}
