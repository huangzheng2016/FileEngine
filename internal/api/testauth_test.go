package api

import (
	"net/http"
)

// serveAuthed runs req through the full router with the server's bearer
// token, as an authenticated API client would.
func serveAuthed(s *Server, w http.ResponseWriter, req *http.Request) {
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	s.Router().ServeHTTP(w, req)
}
