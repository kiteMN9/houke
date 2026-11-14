package protocol

import "testing"

// 选课接口的 flag 语义照 html/zzxkYzbChoosedZy.js 来：1/3 是成功，-1 的 msg
// 是 "分志愿,教学班id,已选人数,本轮已选人数" 的机器串，6/2 是"已选中"/"时间
// 冲突"。改这里之前先看那份脚本对 data.flag 的分支。
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
			result:  ChooseCourseResult{Flag: ChooseFlagCapacityFull, Msg: "0,DO-JXB-1,39,39"},
			display: "该教学班已无余量，不可选！",
		},
		{
			// 页面在这个分支只用固定提示，不看服务端 msg。
			name:    "该教学班已选中",
			result:  ChooseCourseResult{Flag: ChooseFlagAlreadyChosen},
			display: "该教学班已选中，刷新页面可见！",
		},
		{
			name:    "上课时间冲突",
			result:  ChooseCourseResult{Flag: ChooseFlagTimeConflict, Msg: "该教学班与已选课程时间冲突！"},
			display: "该教学班与已选课程时间冲突！",
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
