package protocol

import (
	"testing"

	"school_sdk/protocol/config"
)

// mustNewAPIClient 包装 NewAPIClient：测试里的 baseURL 都是合法的，
// 构造失败说明测试数据写错了，直接 Fatal。
func mustNewAPIClient(t *testing.T, cfg config.Settings, sess *config.Session, isCas2, wx bool, route string) *APIClient {
	t.Helper()
	c, err := NewAPIClient(cfg, sess, isCas2, wx, route)
	if err != nil {
		t.Fatalf("NewAPIClient: %v", err)
	}
	return c
}
