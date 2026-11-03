package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"school_sdk/engine"
	"school_sdk/protocol"
	"school_sdk/utils"
)

// autoNotifyTimeout 是后台发通知邮件的上限：它不应拖住已经结束的 HTTP 请求。
const autoNotifyTimeout = 20 * time.Second

// notifyAutoChooseEnd 在自动选课结束时按服务端配置发一封结果邮件。
//
// 发送条件是：服务端启用 SMTP 且勾选「自动选课结果通知」、该账号自己的
// autoNotify 没有关闭，并且不是用户主动取消。取消不发：那是他刚点过按钮，
// 不需要再收一封邮件；超时、重试用尽、登录失效、内部错误都算"任务结束"，
// 需要通知。发送在后台进行，失败只写日志。
func (s *Server) notifyAutoChooseEnd(id string, course *protocol.CustomCourseDic, taskErr error, ctx context.Context, startedAt time.Time) {
	if s.serverCfg == nil {
		return
	}
	cfg := s.serverCfg.smtp()
	if !cfg.Enabled || !cfg.NotifyAutoChoose {
		return
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return // 用户取消
	}
	account, ok := s.autoNotifyAccount(id)
	if !ok {
		return // 账号关闭了通知，或任务运行期间账号已被删除
	}

	name := id
	switch {
	case strings.TrimSpace(account.Name) != "":
		name = account.Name
	case strings.TrimSpace(account.Username) != "":
		name = account.Username
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	label, detail := autoNotificationStatus(ctx, taskErr, course)
	subject := fmt.Sprintf("[School SDK] 自动选课%s · %s", label, name)
	body := autoNotificationBody(name, label, detail, course, time.Since(startedAt))
	mailCfg := cfg.mailConfig()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), autoNotifyTimeout)
		defer cancel()
		if err := utils.SendSMTP(ctx, mailCfg, subject, body); err != nil {
			log.Printf("发送自动选课通知邮件失败（账号 %s）: %v", name, err)
			return
		}
		log.Printf("自动选课通知邮件已发送（账号 %s，收件人 %s）",
			name, strings.Join(mailCfg.To, ", "))
	}()
}

// autoNotifyAccount 返回该账号是否应接收自动选课结果邮件，以及用于邮件里的
// 账号记录。账号不存在（任务运行期间被删）或显式关闭时 ok=false。
func (s *Server) autoNotifyAccount(id string) (*Account, bool) {
	a, ok := s.store.Get(id)
	if !ok || a == nil || !a.autoNotifyEnabled() {
		return nil, false
	}
	return a, true
}

// autoNotificationStatus 把任务结束原因翻译成邮件里的短标签与说明。
func autoNotificationStatus(ctx context.Context, err error, course *protocol.CustomCourseDic) (label, detail string) {
	if course != nil {
		return "成功", "已选上《" + course.Kcmc + "》。"
	}
	switch {
	case ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "超时", "本次运行时间用尽，尚未选上。"
	case errors.Is(err, engine.ErrNotChosen):
		return "未选上", "重试用尽仍未选上。"
	case errors.Is(err, engine.ErrNoWantedCourse), errors.Is(err, engine.ErrNoCandidates):
		return "失败", "没有可用的候选课程。"
	case errors.Is(err, protocol.ErrUnauthorized):
		return "中断", "登录已失效，任务已中断。"
	case err != nil:
		return "失败", err.Error()
	default:
		return "已结束", "任务已结束。"
	}
}

// autoNotificationBody 生成邮件正文；所有外部字段都经过 HTML 转义。
func autoNotificationBody(name, label, detail string, course *protocol.CustomCourseDic, elapsed time.Duration) string {
	var b strings.Builder
	b.WriteString("<div style=\"font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif;line-height:1.7;color:#0f172a\">")
	fmt.Fprintf(&b, "<p>账号：<b>%s</b></p>", html.EscapeString(name))
	fmt.Fprintf(&b, "<p>结果：<b>%s</b></p>", html.EscapeString(label))
	fmt.Fprintf(&b, "<p>说明：%s</p>", html.EscapeString(detail))
	if course != nil {
		fmt.Fprintf(&b, "<p>课程：<b>%s</b></p>", html.EscapeString(course.Kcmc))
		writeOptionalLine(&b, "教学班", course.Jxbmc)
		writeOptionalLine(&b, "教师", course.Jsxx)
		writeOptionalLine(&b, "时间", course.Sksj)
		writeOptionalLine(&b, "地点", course.Jxdd)
	}
	fmt.Fprintf(&b, "<p>耗时：%s</p>", html.EscapeString(autoTimeoutText(elapsed)))
	b.WriteString("<p style=\"color:#64748b;font-size:12px;margin-top:20px\">")
	b.WriteString("这封邮件由 School SDK 服务端配置中的「自动选课结果通知」发送。")
	b.WriteString("</p></div>")
	return b.String()
}

func writeOptionalLine(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "<p>%s：%s</p>", label, html.EscapeString(value))
}
