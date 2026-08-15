package web

import (
	"net/http"
)

func (s *Server) apiBackupDownload(w http.ResponseWriter, r *http.Request) {
	s.handleBackupDownload(w, r, true)
}

func (s *Server) apiBackupRestore(w http.ResponseWriter, r *http.Request) {
	s.handleRestore(w, r, true)
}
