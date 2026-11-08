package api

import (
	"net/http"
)

// serveAuthed runs req through the full router with the server's bearer
// token, as an authenticated API client would.
func serveAuthed(s *Server, w http.ResponseWriter, req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	s.Router().ServeHTTP(w, req)
}
