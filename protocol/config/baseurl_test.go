package config

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空串", "", ""},
		{"纯空白", "   ", ""},
		{"缺协议", "jwglxt.ycit.edu.cn", "https://jwglxt.ycit.edu.cn/"},
		{"缺协议带登录页", " jwglxt.ycit.edu.cn/xtgl/login_slogin.html ", "https://jwglxt.ycit.edu.cn/"},
		{"主机无斜杠", "https://jwglxt.ycit.edu.cn", "https://jwglxt.ycit.edu.cn/"},
		{"登录页收敛到根", "https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html", "https://jwglxt.ycit.edu.cn/"},
		{"首页收敛到根", "https://jwglxt.ycit.edu.cn/xtgl/index_initMenu.html?jsdm=xs", "https://jwglxt.ycit.edu.cn/"},
		{"保留子路径", "https://jwglxt.ycit.edu.cn/jwglxt", "https://jwglxt.ycit.edu.cn/jwglxt/"},
		{"保留子路径并去掉登录页", "https://jwglxt.ycit.edu.cn/jwglxt/xtgl/login_slogin.html?time=1", "https://jwglxt.ycit.edu.cn/jwglxt/"},
		{"子路径缺协议", "jwglxt.ycit.edu.cn/jwglxt/", "https://jwglxt.ycit.edu.cn/jwglxt/"},
		{"去查询与片段", "https://jwglxt.ycit.edu.cn/jwglxt/?x=1#frag", "https://jwglxt.ycit.edu.cn/jwglxt/"},
		{"保留端口", "http://127.0.0.1:8080/base", "http://127.0.0.1:8080/base/"},
		{"非 http 协议不猜", "ftp://example.com/x", "ftp://example.com/x/"},
		{"解析失败原样返回", "://bad", "://bad"},
	}
	for _, c := range cases {
		if got := NormalizeBaseURL(c.in); got != c.want {
			t.Errorf("%s: NormalizeBaseURL(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	ok := []string{
		"https://jwglxt.ycit.edu.cn/",
		"http://127.0.0.1:8080/",
		"https://jwglxt.ycit.edu.cn",
		"https://jwglxt.ycit.edu.cn/jwglxt/",
		"https://jwglxt.ycit.edu.cn/jwglxt",
		"http://127.0.0.1:8080/base/",
	}
	for _, s := range ok {
		if err := ValidateBaseURL(s); err != nil {
			t.Errorf("ValidateBaseURL(%q) = %v, want nil", s, err)
		}
	}

	bad := []string{
		"",
		"jwglxt.ycit.edu.cn/",
		"ftp://jwglxt.ycit.edu.cn/",
		"https://jwglxt.ycit.edu.cn/xtgl/login_slogin.html",
		"https://jwglxt.ycit.edu.cn/jwglxt/xtgl/index_initMenu.html",
		"https://jwglxt.ycit.edu.cn/jwglxt/?time=1",
		"https://jwglxt.ycit.edu.cn/jwglxt/#top",
		"https://jwglxt.ycit.edu.cn/a/../b/",
		"https:///",
	}
	for _, s := range bad {
		if err := ValidateBaseURL(s); err == nil {
			t.Errorf("ValidateBaseURL(%q) = nil, want error", s)
		}
	}
}
