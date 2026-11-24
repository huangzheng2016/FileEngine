package db

const (
	DefaultPageSize = 20
	MaxPageSize     = 50
)

// Page turns a request's 1-based page and size into FindByPage arguments.
// Missing or out-of-range values fall back to page 1 and the default size.
func Page(page, size int32) (offset, limit int) {
	if size <= 0 {
		size = DefaultPageSize
	}
	if size > MaxPageSize {
		size = MaxPageSize
	}
	if page < 1 {
		page = 1
	}
	return int(page-1) * int(size), int(size)
}
