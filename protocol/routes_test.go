package protocol

import (
	"slices"
	"testing"
	"time"

	"school_sdk/protocol/config"
)

func TestRouteCandidatesMergeUserAndLearned(t *testing.T) {
	cfg := config.Settings{Routes: []string{"user-1"}}
	sess := config.NewSession()
	sess.LearnRoute("user-1") // 与自定义重复，应去重
	sess.LearnRoute("learned-1")

	got := routeCandidates(cfg, sess)
	if want := []string{"user-1", "learned-1"}; !slices.Equal(got, want) {
		t.Fatalf("候选池 = %v, want %v", got, want)
	}

	// 返回的必须是副本：调用方改结果不能污染配置。
	got[0] = "mutated"
	if cfg.Routes[0] != "user-1" {
		t.Fatalf("候选池与 cfg.Routes 共享了底层数组: %v", cfg.Routes)
	}
}

func TestRouteCandidatesDefaultPool(t *testing.T) {
	got := routeCandidates(config.Settings{}, nil)
	if len(got) == 0 {
		t.Fatal("没有自定义 route 时应回落到内置默认池")
	}
	if len(routeCandidates(config.Default(), config.NewSession())) == 0 {
		t.Fatal("默认配置 + 空会话也应给出候选池")
	}
}

// 重启场景：同一个 Session 注入新客户端，学到的东西还在；
// 传 nil 时客户端自己兜底，不能 panic。
func TestNewAPIClientKeepsSession(t *testing.T) {
	cfg := config.Settings{
		BaseURL:   "https://jwglxt.ycit.edu.cn/",
		Account:   "u",
		Password:  "p",
		UserAgent: config.ChromeUA,
		Timeout:   config.Duration(5 * time.Second),
	}
	sess := config.NewSession()
	sess.LearnRoute("learned-keep")

	c := mustNewAPIClient(t, cfg, sess, false, false, "")
	if got := c.Session().LearnedRoutes; !slices.Equal(got, []string{"learned-keep"}) {
		t.Fatalf("新客户端丢了会话产物: %v", got)
	}

	c2 := mustNewAPIClient(t, cfg, nil, false, false, "")
	if got := c2.Session(); got.TicketJWT != "" || len(got.LearnedRoutes) != 0 {
		t.Fatalf("nil 会话应得到空快照: %+v", got)
	}
	c2.sessionOrNew().LearnRoute("r")
	if got := c2.Session().LearnedRoutes; !slices.Equal(got, []string{"r"}) {
		t.Fatalf("nil 会话兜底后写不进去: %v", got)
	}
}
