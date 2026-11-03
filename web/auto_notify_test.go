package web

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"school_sdk/engine"
	"school_sdk/protocol"
)

func TestAutoNotificationStatus(t *testing.T) {
	course := &protocol.CustomCourseDic{Kcmc: "高等数学"}
	if label, _ := autoNotificationStatus(context.Background(), nil, course); label != "成功" {
		t.Errorf("成功任务 label = %q", label)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-timeoutCtx.Done()
	if label, _ := autoNotificationStatus(timeoutCtx, context.DeadlineExceeded, nil); label != "超时" {
		t.Errorf("超时 label = %q", label)
	}
	if label, _ := autoNotificationStatus(context.Background(), engine.ErrNotChosen, nil); label != "未选上" {
		t.Errorf("ErrNotChosen label = %q", label)
	}
	if label, _ := autoNotificationStatus(context.Background(), protocol.ErrUnauthorized, nil); label != "中断" {
		t.Errorf("ErrUnauthorized label = %q", label)
	}
	if label, _ := autoNotificationStatus(context.Background(), errors.New("boom"), nil); label != "失败" {
		t.Errorf("未知错误 label = %q", label)
	}
}

// 自动选课邮件受账号级开关控制：默认发送，显式关闭后不发，账号不存在也不发。
func TestAutoNotifyAccountSwitch(t *testing.T) {
	store := newTestStore(t)
	srv := mustNewServer(t, store)
	acc, err := store.Create(&Account{
		Name:     "n",
		Username: "u",
		BaseURL:  "https://jwglxt.ycit.edu.cn/",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, ok := srv.autoNotifyAccount(acc.ID); !ok {
		t.Fatal("默认应发送自动选课邮件")
	}
	if _, err := store.SetAutoNotify(acc.ID, false); err != nil {
		t.Fatalf("SetAutoNotify: %v", err)
	}
	if _, ok := srv.autoNotifyAccount(acc.ID); ok {
		t.Error("账号关闭 autoNotify 后仍会发送")
	}
	if _, ok := srv.autoNotifyAccount("missing"); ok {
		t.Error("账号不存在时不应发送")
	}
}

func TestAutoNotificationBodyEscapesHTML(t *testing.T) {
	body := autoNotificationBody(
		`<script>alert(1)</script>`,
		"成功",
		`a & b`,
		&protocol.CustomCourseDic{
			Kcmc: "<b>课程</b>",
			Jsxx: "<img src=x onerror=alert(1)>",
		},
		time.Minute,
	)
	if strings.Contains(body, "<script>") || strings.Contains(body, "<img src=x") {
		t.Errorf("邮件正文没有转义用户输入:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "&lt;b&gt;课程&lt;/b&gt;") {
		t.Errorf("邮件正文缺少转义后的内容:\n%s", body)
	}
}
