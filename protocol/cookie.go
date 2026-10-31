package protocol

import (
	"net/url"
	"strings"
)

// CookieSnapshot 是当前会话 Cookie 的可序列化快照。
//
// Value 是真实的 Cookie 值，属于敏感信息，只应返回给本机 WebUI。
type CookieSnapshot struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Domain    string `json:"domain"`
	Path      string `json:"path"`
	ExpiresAt int64  `json:"expiresAt"` // Unix 毫秒，0 表示会话 Cookie
	Secure    bool   `json:"secure"`
	HTTPOnly  bool   `json:"httpOnly"`
}

// Cookies 返回当前 BaseURL 下的 Cookie 列表和可直接用于请求头的字符串。
// 不发起网络请求，也不要求处于登录态。
func (a *APIClient) Cookies() ([]CookieSnapshot, string) {
	if a == nil || a.Http == nil {
		return []CookieSnapshot{}, ""
	}
	targetURL, err := url.Parse(a.Http.BaseURL())
	if err != nil {
		return []CookieSnapshot{}, ""
	}
	jar := a.Http.CookieJar()
	if jar == nil {
		return []CookieSnapshot{}, ""
	}
	cookies := jar.Cookies(targetURL)
	out := make([]CookieSnapshot, 0, len(cookies))
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		var expiresAt int64
		if !c.Expires.IsZero() {
			expiresAt = c.Expires.UnixMilli()
		}
		out = append(out, CookieSnapshot{
			Name:      c.Name,
			Value:     c.Value,
			Domain:    c.Domain,
			Path:      c.Path,
			ExpiresAt: expiresAt,
			Secure:    c.Secure,
			HTTPOnly:  c.HttpOnly,
		})
		parts = append(parts, c.Name+"="+c.Value)
	}
	return out, strings.Join(parts, "; ")
}
