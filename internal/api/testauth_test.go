package api

import (
	"net/http"
)

func serveAuthed(s *Server, w http.ResponseWriter, req *http.Request) {
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	s.Router().ServeHTTP(w, req)
}

func authedHandler(s *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.opts.Token)
		s.Router().ServeHTTP(w, r)
	})
}
