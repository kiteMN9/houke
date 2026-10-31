package web

import (
	"testing"

	"school_sdk/protocol"
)

func TestInitStageText(t *testing.T) {
	cases := []struct {
		name  string
		stage protocol.InitStage
		want  string
	}{
		{"取首页参数", protocol.InitStage{Phase: protocol.InitPhaseIndex}, "正在获取选课首页参数"},
		{"等待开放", protocol.InitStage{Phase: protocol.InitPhaseIndexWait}, "等待选课阶段开放"},
		{"解析页签", protocol.InitStage{Phase: protocol.InitPhaseIndexParse}, "正在解析选课页签"},
		{"取列表参数", protocol.InitStage{Phase: protocol.InitPhaseListPre}, "正在获取课程列表参数"},
		{"解析规则", protocol.InitStage{Phase: protocol.InitPhaseListPreParse}, "正在解析选课规则"},
		{"未知阶段返回空", protocol.InitStage{Phase: "init.unknown"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := initStageText(tc.stage); got != tc.want {
				t.Errorf("initStageText(%+v) = %q, want %q", tc.stage, got, tc.want)
			}
		})
	}
}
