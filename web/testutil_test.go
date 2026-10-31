package web

import (
	"path/filepath"
	"testing"
)

// mustNewServer 包装 NewServer：静态资源是编译期嵌入的，测试里不应失败。
func mustNewServer(t *testing.T, store *AccountStore) *Server {
	t.Helper()
	s, err := NewServer(store)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

// mustBuildRuntime 包装 buildRuntime：调用方已经建好合法账号。
func mustBuildRuntime(t *testing.T, s *AccountStore, a *Account) *accountRuntime {
	t.Helper()
	rt, err := buildRuntime(s, a.ID, a)
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	return rt
}

func newTestStore(t *testing.T) *AccountStore {
	t.Helper()
	s, err := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatalf("NewAccountStore: %v", err)
	}
	return s
}
