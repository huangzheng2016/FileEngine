package api

import (
	"strconv"
	"time"

	"FileEngine/biz/errs"
)

// Conversions DAL -> API and request helpers shared by the handlers of this
// package. Per-feature conversions go into convert_<feature>.go next to the
// handler file, never onto the model types and never into the hz-generated
// *_service.go files.

// unix is the IDL's time: seconds, with 0 meaning "none" (a zero time.Time is
// year 1, whose Unix value is not 0).
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// unixPtr is unix for nullable database times.
func unixPtr(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return unix(*t)
}

// id turns a request id into the database's uint id. Ids are positive, so 0 and
// negative values (which would wrap) are a bad request.
func id(v int64) (uint, error) {
	if v <= 0 {
		return 0, errs.BadRequest
	}
	return uint(v), nil
}

// optID is id for fields where 0 means "none".
func optID(v int64) (uint, error) {
	if v < 0 {
		return 0, errs.BadRequest
	}
	return uint(v), nil
}

// parseIDs parses a list<string> of ids (the json ",string" option does not
// apply to lists). At most max entries; every entry must be a positive number.
func parseIDs(raw []string, max int) ([]uint, error) {
	if len(raw) > max {
		return nil, errs.BadRequest
	}
	out := make([]uint, len(raw))
	for i, s := range raw {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil || n == 0 {
			return nil, errs.BadRequest
		}
		out[i] = uint(n)
	}
	return out, nil
}
