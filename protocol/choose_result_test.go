package protocol

import "testing"

// 选课接口的 flag 语义以 html/_xkBcZyZzxkYzb.html 的错误样本为准：1/3 是成功；
// -1 的 msg 是 "分志愿,教学班id,已选人数,本轮已选人数" 的机器串；6 是"该教学班
// 已选中"；时间冲突、一门课只能选一个教学班、窗口关闭、门次限制、非法操作、
// 加密串过期都是 0 + 文案。
func TestChooseCourseResultSemantics(t *testing.T) {
	cases := []struct {
		name    string
		result  ChooseCourseResult
		ok      bool
		display string
	}{
		{
			name:   "选课成功",
			result: ChooseCourseResult{Flag: ChooseFlagOK},
			ok:     true,
		},
		{
			name:    "成功但免费学分已达上限",
			result:  ChooseCourseResult{Flag: ChooseFlagOKFreeCreditLimit, Msg: "选课成功，免费学分已达上限！"},
			ok:      true,
			display: "选课成功，免费学分已达上限！",
		},
		{
			name:    "容量超出",
			result:  ChooseCourseResult{Flag: ChooseFlagCapacityFull, Msg: "0,DO-JXB-1,39,"},
			display: "该教学班已无余量，不可选！",
		},
		{
			// 页面在这个分支只用固定提示，不看服务端 msg。
			name:    "该教学班已选中",
			result:  ChooseCourseResult{Flag: ChooseFlagAlreadyChosen},
			display: "该教学班已选中，刷新页面可见！",
		},
		{
			name:    "上课时间冲突（样本走 0）",
			result:  ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "所选教学班的上课时间与其他教学班有冲突！"},
			display: "所选教学班的上课时间与其他教学班有冲突！",
		},
		{
			name:    "普通业务拒绝",
			result:  ChooseCourseResult{Flag: "0", Msg: "超过学分上限！"},
			display: "超过学分上限！",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.OK(); got != tc.ok {
				t.Fatalf("OK() = %v，期望 %v", got, tc.ok)
			}
			if got := tc.result.DisplayMsg(); got != tc.display {
				t.Fatalf("DisplayMsg() = %q，期望 %q", got, tc.display)
			}
		})
	}
}

// flag=0 里只有"选课频率过高"值得重试；其它业务拒绝是确定性的，自动选课要靠
// Retryable 停下来（见 engine.ErrChooseRejected）。
func TestChooseCourseResultRetryable(t *testing.T) {
	cases := []struct {
		name   string
		result ChooseCourseResult
		want   bool
	}{
		{"中文频率限制", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "选课频率过高，请稍后重试！"}, true},
		{"英文频率限制", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "The frequency of course selection is too high, please try again in 20 seconds!"}, true},
		{"频率限制带前后缀", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "提示：选课频率过高，请稍后重试！(20s)"}, true},
		{"学分上限", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "超过学分上限！"}, false},
		{"门次限制", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "超过通识选修课本学期本专业最高选课门次限制，不可选！"}, false},
		{"不在选课时间", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "不在选课时间内！"}, false},
		{"上课时间冲突", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "所选教学班的上课时间与其他教学班有冲突！"}, false},
		{"一门课只能选一个教学班", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "一门课程只能选一个教学班，不可再选！"}, false},
		{"非法操作", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "警告:你正在非法操作！"}, false},
		{"加密串过期（Choose 里已续期过）", ChooseCourseResult{Flag: ChooseFlagRejected, Msg: "加密串错误，可以清除浏览器缓存后刷新网页重试！"}, false},
		{"空文案", ChooseCourseResult{Flag: ChooseFlagRejected}, false},
		{"容量超出可重试", ChooseCourseResult{Flag: ChooseFlagCapacityFull, Msg: "0,DO-JXB-1,39,"}, true},
		{"已选中仍按可重试处理", ChooseCourseResult{Flag: ChooseFlagAlreadyChosen}, true},
		{"成功", ChooseCourseResult{Flag: ChooseFlagOK}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.Retryable(); got != tc.want {
				t.Fatalf("Retryable() = %v，期望 %v", got, tc.want)
			}
		})
	}
}
