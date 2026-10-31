package web

import (
	"slices"
	"testing"
)

func TestAccountSessionRoundTrip(t *testing.T) {
	a := &Account{
		BaseURL:       "https://a.example/",
		TicketJWT:     "jwt-1",
		LearnedRoutes: []string{"r1"},
	}

	sess := a.session()
	if sess.TicketJWT != "jwt-1" || !slices.Equal(sess.Routes(), []string{"r1"}) {
		t.Fatalf("会话没装对: %+v", sess)
	}

	// 模拟登录期间 client 做的事：刷新 JWT、学到新 route、修正地址。
	sess.TicketJWT = "jwt-2"
	sess.LearnRoute("r2")
	sess.CorrectedBaseURL = "https://b.example/"

	a.applySession(sess.Snapshot())
	if a.TicketJWT != "jwt-2" {
		t.Fatalf("JWT 没落盘: %q", a.TicketJWT)
	}
	if !slices.Equal(a.LearnedRoutes, []string{"r1", "r2"}) {
		t.Fatalf("学到的 route 没落盘: %v", a.LearnedRoutes)
	}
	if a.BaseURL != "https://b.example/" {
		t.Fatalf("修正后的地址没落盘: %q", a.BaseURL)
	}

	// 没有修正地址时，不能把已有地址清空；JWT 被清空（过期）则要如实落盘。
	sess.Clear()
	a.applySession(sess.Snapshot())
	if a.BaseURL != "https://b.example/" {
		t.Fatalf("空 CorrectedBaseURL 覆盖了已有地址: %q", a.BaseURL)
	}
	if a.TicketJWT != "" {
		t.Fatalf("过期 JWT 没被清掉: %q", a.TicketJWT)
	}
}

func TestAccountSessionDoesNotAliasStore(t *testing.T) {
	a := &Account{LearnedRoutes: []string{"r1"}}
	sess := a.session()

	// client 在登录 goroutine 里改会话，不能影响 store 持有的 Account。
	sess.LearnRoute("r2")
	sess.TicketJWT = "jwt"
	if len(a.LearnedRoutes) != 1 || a.TicketJWT != "" {
		t.Fatalf("会话与 Account 共享了状态: %+v / %+v", a.LearnedRoutes, a.TicketJWT)
	}
}
