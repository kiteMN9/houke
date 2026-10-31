package config

import (
	"slices"
	"testing"
)

func TestSessionLearnAndForget(t *testing.T) {
	s := NewSession()
	if got := s.Routes(); len(got) != 0 {
		t.Fatalf("新会话不该有 route: %v", got)
	}

	s.LearnRoute("a")
	s.LearnRoute("b")
	// 同一 route 再学一次：先移除旧的再追加，最近学到的排最后。
	s.LearnRoute("a")
	if got, want := s.Routes(), []string{"b", "a"}; !slices.Equal(got, want) {
		t.Fatalf("LearnRoute 之后 = %v, want %v", got, want)
	}

	s.ForgetRoute("b")
	if got, want := s.Routes(), []string{"a"}; !slices.Equal(got, want) {
		t.Fatalf("ForgetRoute 之后 = %v, want %v", got, want)
	}

	// 忘记不存在的 route：不 panic，也不改动集合。
	s.ForgetRoute("nope")
	if got, want := s.Routes(), []string{"a"}; !slices.Equal(got, want) {
		t.Fatalf("ForgetRoute(不存在) 之后 = %v, want %v", got, want)
	}

	s.Clear()
	if got := s.Routes(); len(got) != 0 {
		t.Fatalf("Clear 之后 = %v，应为空", got)
	}
}

func TestSessionSnapshotIsIsolated(t *testing.T) {
	s := NewSession()
	s.TicketJWT = "jwt"
	s.CorrectedBaseURL = "https://fixed/"
	s.LearnRoute("r1")

	snap := s.Snapshot()
	if snap.TicketJWT != "jwt" || snap.CorrectedBaseURL != "https://fixed/" {
		t.Fatalf("快照丢字段: %+v", snap)
	}

	// 改快照不影响会话。
	snap.LearnedRoutes[0] = "mutated"
	if got := s.Routes()[0]; got != "r1" {
		t.Fatalf("改快照影响了会话: %q", got)
	}

	// 改会话也不影响已经取走的快照。
	s.LearnRoute("r2")
	if got := snap.LearnedRoutes; !slices.Equal(got, []string{"mutated"}) {
		t.Fatalf("会话后续写入漏进了旧快照: %v", got)
	}

	// Routes() 同样必须是副本。
	routes := s.Routes()
	routes[0] = "hacked"
	if got := s.Routes()[0]; got != "r1" {
		t.Fatalf("Routes() 返回了内部切片: %q", got)
	}
}
