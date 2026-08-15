// M8.5 backup UI: GET /backups is the upload page. The operator
// downloads a fresh tar.zst (POST /backups/download) or uploads one
// (POST /backups/restore). There is no web listing, create, named
// download or delete — those dump plaintext SQLite into ./backups
// without a UI. CLI backup create/list/restore remains for DR.
//
// Security invariants (same as M6_AUDIT.md §5):
//   - the page renders no secrets; flash messages are fixed strings
//     that never echo user input, and internal errors are logged
//     server-side and answered with fixed flash messages or the
//     generic error page;
//   - one-click download answers attachment-only bytes
//     (Content-Disposition), never HTML.
package web

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/amnezia-vpn/amnezia-vpn-server/internal/backup"
)

// Flash messages are fixed strings; they never contain request input,
// names, paths or secrets. The whole panel is Russian (T-120 round 2).
const (
	// flashRestoreBlockedByPending is shown when download is attempted
	// while a restore is pending (M8.6: no mutation while a restore
	// is prepared).
	flashRestoreBlockedByPending = "Восстановление уже подготовлено. Требуется перезапуск."
	flashBackupDownloadFailed    = "Не удалось скачать бэкап."
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

// backupsData is the GET /backups payload. It carries no archive
// listing: the page is the upload form only.
type backupsData struct {
	Flash    string
	Username string
	CSRF     string
	// Pending reflects an existing .restore-pending marker: the page
	// shows the restart-required banner.
	Pending bool
}

// flashBackups completes the PRG cycle towards /backups with a fixed
// message.
func (s *Server) flashBackups(w http.ResponseWriter, r *http.Request, msg string) {
	redirect303(w, r, "/backups?msg="+url.QueryEscape(msg))
}

// backupDownloadNow handles POST /backups/download (T-125/T-143): a
// fresh tar.zst of the live database is created in a private temp
// directory, streamed as an attachment and removed — nothing is stored
// in backups/. CSRF-protected; paused while a restore is pending.
func (s *Server) backupDownloadNow(w http.ResponseWriter, r *http.Request) {
	s.handleBackupDownload(w, r, false)
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request, jsonAPI bool) {
	if pending, ok := s.pendingExistsReply(w, jsonAPI); !ok {
		return
	} else if pending {
		if jsonAPI {
			writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "message": flashRestoreBlockedByPending})
			return
		}
		s.flashBackups(w, r, flashRestoreBlockedByPending)
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	fail := func(what string, err error) {
		s.cfg.Logger.Printf("%s: %v", what, err)
		if jsonAPI {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "message": flashBackupDownloadFailed})
			return
		}
		s.flashBackups(w, r, flashBackupDownloadFailed)
	}
	tmp, err := os.MkdirTemp("", "panel-download-*")
	if err != nil {
		fail("backup download: temp dir", err)
		return
	}
	defer os.RemoveAll(tmp)
	archive, err := backup.Create(s.db(), tmp, time.Now)
	if err != nil {
		fail("backup download: create", err)
		return
	}
	f, err := os.Open(archive)
	if err != nil {
		fail("backup download: open", err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		fail("backup download: stat", err)
		return
	}
	// backup.Create returns a bare archive name (backup-YYYY-MM-DD.tar.zst).
	name := filepath.Base(archive)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	if _, err := io.Copy(w, f); err != nil {
		s.cfg.Logger.Printf("backup download: stream: %v", err)
	}
}
