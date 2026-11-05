package engine

import (
	"testing"

	"school_sdk/protocol"
	"school_sdk/protocol/config"
)

// TestNewPassesTransportPool 验证 Options.Transports 真正下发到 protocol：
// 两个 Engine 共用一个池时应复用同一个底层 transport，而 cookie jar 仍各自独立。
func TestNewPassesTransportPool(t *testing.T) {
	cfg := config.Settings{BaseURL: "https://jwglxt.ycit.edu.cn/"}
	pool := protocol.NewTransportPool()

	e1, err := New(cfg, nil, LoginPassword, Options{Transports: pool})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e2, err := New(cfg, nil, LoginPassword, Options{Transports: pool})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e1.client.Http.Transport() == nil {
		t.Fatal("transport 不应为 nil")
	}
	if e1.client.Http.Transport() != e2.client.Http.Transport() {
		t.Fatal("共用 TransportPool 时应共用 transport")
	}
	if e1.client.Http.Client().Jar == e2.client.Http.Client().Jar {
		t.Fatal("cookie jar 仍必须每账号独立")
	}
}

// TestNewWithoutTransportPoolKeepsIsolation 验证不传池时行为不变：
// 每个 Engine 自建 transport。
func TestNewWithoutTransportPoolKeepsIsolation(t *testing.T) {
	cfg := config.Settings{BaseURL: "https://jwglxt.ycit.edu.cn/"}
	e1, err := New(cfg, nil, LoginPassword, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e2, err := New(cfg, nil, LoginPassword, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e1.client.Http.Transport() == e2.client.Http.Transport() {
		t.Fatal("不传池时不应共享 transport")
	}
}
