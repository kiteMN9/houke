package protocol

import (
	"context"
	"sync"
	"testing"
	"time"
)

// constTestBaseURL 只是为了让 NewAPIClient 通过 baseURL 校验；这些测试不访问网络。
const constTestBaseURL = "http://127.0.0.1:1"

// 冷静期属于账号自己：A 刚登录成功，不能把 B 也判成"刚登录过"。
// 以前 lastSuccessTime 是包级的，B 会因此拿到一个假的"重登成功"。
func TestLoginCooldownIsPerClient(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	b := newStateTestClient(t, constTestBaseURL)

	now := time.Now()
	a.login.lastSuccess = now

	if !a.inLoginCooldown(now) {
		t.Error("刚登录成功的账号应处于冷静期")
	}
	if b.inLoginCooldown(now) {
		t.Error("另一个账号不该继承 A 的冷静期")
	}
	if b.inLoginCooldown(now.Add(2 * time.Second)) {
		t.Error("从未登录过的账号不该处于冷静期")
	}

	b.login.lastSuccess = now
	if !b.inLoginCooldown(now) {
		t.Error("B 自己登录成功后应处于冷静期")
	}
	if !a.inLoginCooldown(now.Add(loginCooldown / 2)) {
		t.Error("冷静期未结束时应仍处于冷静期")
	}
	if a.inLoginCooldown(now.Add(loginCooldown)) {
		t.Error("冷静期结束后不该再命中")
	}
}

// 登录进行中时，业务请求应等在闸门上，登录结束后被唤醒。
func TestLoginGateBlocksUntilLoginEnds(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	end := a.login.gate.begin()

	done := make(chan bool, 1)
	go func() {
		done <- a.waitLogin(context.Background())
	}()

	select {
	case <-done:
		t.Fatal("登录结束前 waitLogin 就返回了")
	case <-time.After(50 * time.Millisecond):
	}

	end()

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("等待期间 ctx 未取消，waitLogin 应返回 true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("登录结束后 waitLogin 没有被唤醒")
	}
}

// 等待期间 ctx 取消，waitLogin 要能提前退出，否则 Web 端取消任务会被登录拖住。
func TestLoginGateHonorsContext(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	end := a.login.gate.begin()
	defer end()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if a.waitLogin(ctx) {
		t.Error("ctx 已取消，waitLogin 应返回 false")
	}
}

// 没有登录在进行时，waitLogin 必须立即放行（绝大多数请求都走这条路径）。
func TestWaitLoginReturnsImmediatelyWhenIdle(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)

	start := time.Now()
	if !a.waitLogin(context.Background()) {
		t.Fatal("空闲时 waitLogin 应返回 true")
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("空闲时 waitLogin 耗时 %v，不应阻塞", d)
	}
}

// 同一账号的登录入口必须排队：LoginWithContext 不能绕过正在进行的 ReLogin，
// 否则两条登录链路会并发跑，cookie 互相覆盖。
func TestLoginSerializesSameClient(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)

	a.login.mu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 拿到锁之后 doLogin 会立刻返回 false，不会访问网络

	returned := make(chan bool, 1)
	go func() {
		returned <- a.LoginWithContext(ctx)
	}()

	select {
	case <-returned:
		a.login.mu.Unlock()
		t.Fatal("登录锁被占用时 LoginWithContext 提前返回了")
	case <-time.After(50 * time.Millisecond):
	}

	a.login.mu.Unlock()
	select {
	case ok := <-returned:
		if ok {
			t.Error("ctx 已取消，登录应返回 false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("解锁后 LoginWithContext 没有返回")
	}
}

// 不同账号的登录互不阻塞：A 卡在登录里时，B 应该能立刻进入。
// 这是以前全局 loginMU 最直接的副作用。
func TestLoginDoesNotBlockOtherClients(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	b := newStateTestClient(t, constTestBaseURL)

	a.login.mu.Lock()
	defer a.login.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if b.LoginWithContext(ctx) {
		t.Error("ctx 已取消，B 的登录应失败")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("B 的登录被 A 阻塞了 %v", d)
	}
}

// 闸门会被并发读写（请求 goroutine 等、登录 goroutine 关），这里做一次竞态覆盖。
func TestLoginGateConcurrentAccess(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			end := a.login.gate.begin()
			time.Sleep(time.Millisecond)
			end()
		}()
		go func() {
			defer wg.Done()
			a.waitLogin(ctx)
		}()
	}
	wg.Wait()

	if !a.waitLogin(ctx) {
		t.Error("所有登录结束后 waitLogin 应返回 true")
	}
}

// 滑块标记属于账号自己：A 见过滑块，不该把 B 也带到滑块分支。
func TestSliderCaptchaIsPerClient(t *testing.T) {
	a := newStateTestClient(t, constTestBaseURL)
	b := newStateTestClient(t, constTestBaseURL)

	a.setSliderCaptcha(true)
	if !a.hasSliderCaptcha() {
		t.Error("A 应记录自己见过滑块验证码")
	}
	if b.hasSliderCaptcha() {
		t.Error("B 不该继承 A 的滑块标记")
	}
}
