package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// pagePaths 是用户最容易整条贴错的页面地址。它们不能当请求根（拼出来会变成
// .../login_slogin.html/xtgl/...），但可以据此反推出站点前缀（例如 /jwglxt）。
var pagePaths = []string{LoginIndex, MENU, ChooseCourseIndex}

// ValidateBaseURL 检查 baseURL 是否可以安全地作为请求根地址。
//
// 允许带路径前缀（例如 https://host/jwglxt/）：resty 会把以 "/" 开头的请求
// 路径直接拼在前缀后面（见 resty middleware 的 composed = TrimRight(baseURL,"/") + url），
// 所以目录部署在 /jwglxt 这类前缀下也能正常访问。不允许的是页面地址
// （登录页 / 首页）、查询参数和 # 片段——它们会让拼接结果错位。
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url 非法: %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url 必须以 http:// 或 https:// 开头: %q", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("url 缺少主机名: %q", raw)
	}
	if u.RawQuery != "" {
		return fmt.Errorf("url 不要带查询参数: %q", raw)
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return fmt.Errorf("url 不要带 # 片段: %q", raw)
	}
	for _, page := range pagePaths {
		if strings.Contains(u.Path, page) {
			return fmt.Errorf("url 里不要带页面路径 %q，填到它之前即可（例如 https://%s/jwglxt/）: %q", page, u.Host, raw)
		}
	}
	if slices.Contains(strings.Split(u.Path, "/"), "..") {
		return fmt.Errorf("url 路径里不能有 %q: %q", "..", raw)
	}
	return nil
}

// NormalizeBaseURL 把用户填的 / 历史配置里的地址收敛成 “协议://主机[:端口]/前缀/”。
//
// 保留子路径前缀（/jwglxt 这类目录部署），只做四件事：缺协议补 https、去掉
// 登录页/首页路径及其尾巴、去掉查询与片段、结尾补 "/"（resty 拼接时会 TrimRight，
// 补不补都能用，统一补上是为了存储和展示稳定）。解析失败时原样返回，
// 交给 ValidateBaseURL 报错，不替用户猜。
func NormalizeBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}

	path := u.Path
	for _, page := range pagePaths {
		if idx := strings.Index(path, page); idx >= 0 {
			path = path[:idx]
		}
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	u.Path = path
	u.RawPath = ""
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}
