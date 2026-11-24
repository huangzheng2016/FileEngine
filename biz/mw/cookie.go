package mw

import (
	"FileEngine/biz/service/auth"
	"FileEngine/container"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol"
)

// SetSessionCookie issues the HttpOnly SameSite=Strict cookie that lets
// EventSource and fetch reach the API same-origin without exposing the token to
// page scripts.
func SetSessionCookie(c *app.RequestContext) {
	c.SetCookie(auth.SessionCookie, container.Default.Auth.Token(), 0, "/", "",
		protocol.CookieSameSiteStrictMode, string(c.URI().Scheme()) == "https", true)
}

func ClearSessionCookie(c *app.RequestContext) {
	c.SetCookie(auth.SessionCookie, "", -1, "/", "", protocol.CookieSameSiteStrictMode, false, true)
}
