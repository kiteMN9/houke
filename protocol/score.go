package protocol

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	baseCfg "school_sdk/protocol/config"
	"strconv"
	"time"
)

// WebGetScore 供 Web 层调用的成绩查询。
// xnm: 学年，如 "2024"；xqm: 学期原始值，如 "3"/"12"/"16"。
// 与 getScoreRaw 的区别：接受 context、返回 error、重试次数有上限。
func (a *APIClient) WebGetScore(ctx context.Context, xnm, xqm string) ([]Score, error) {
	if xnm == "" || xqm == "" {
		return nil, fmt.Errorf("xnm / xqm 不能为空")
	}

	var score ScoreRaw
	for i := 0; i < 5; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetQueryParams(map[string]string{
				"doType": "query",
				"gnmkdm": "N305005",
				"su":     a.cfg.Account,
			}).
			SetFormData(map[string]string{
				"xnm":                    xnm,
				"xqm":                    xqm,
				"_search":                "false",
				"nd":                     strconv.FormatInt(time.Now().UnixMilli(), 10),
				"queryModel.showCount":   "500",
				"queryModel.currentPage": "1",
				"queryModel.sortName":    "",
				"queryModel.sortOrder":   "asc",
				"time":                   "0",
			}).
			Post(baseCfg.SCORE)

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return nil, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("查询成绩超时")
			}
			log.Println("成绩请求错误:", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if resp.IsStatusFailure() {
			return nil, fmt.Errorf("成绩查询失败: %s", resp.Status())
		}

		// 权限拒绝的页面不带当前账号，LoginCheck 可能误判成“掉线”，
		// 所以先认“无功能权限”，再判断登录态。
		if isNoPermission(resp.String()) {
			log.Println("成绩查询无功能权限")
			return nil, ErrNoPermission
		}

		if !a.LoginCheck(resp) {
			return nil, ErrUnauthorized
		}

		if err := json.Unmarshal(resp.Bytes(), &score); err != nil {
			return nil, fmt.Errorf("成绩解析失败: %w", err)
		}
		return score.Items, nil
	}
	return nil, fmt.Errorf("成绩查询失败：重试次数已用尽")
}

// GetSuggestYearTerm 返回成绩/课表查询的默认学年与学期（按当前月份猜）。
func GetSuggestYearTerm() (string, int) {
	now := time.Now()
	month := now.Month()
	var year = ""
	var term int
	switch {
	case month >= 1 && month <= 6:
		year = strconv.Itoa(now.Year() - 1)
		term = 1
	case month >= 7 && month <= 12:
		year = strconv.Itoa(now.Year() - 1)
		term = 2
	}
	return year, term
}

type ScoreRaw struct {
	CurrentPage   int     `json:"currentPage"`
	CurrentResult int     `json:"currentResult"`
	EntityOrField bool    `json:"entityOrField"`
	Items         []Score `json:"items"`
	Limit         int     `json:"limit"`
	Offset        int     `json:"offset"`
	PageNo        int     `json:"pageNo"`
	PageSize      int     `json:"pageSize"`
	ShowCount     int     `json:"showCount"`
	TotalCount    int     `json:"totalCount"`
	TotalPage     int     `json:"totalPage"`
	TotalResult   int     `json:"totalResult"`
}
type Score struct {
	//# njdm_id -> 年级代码
	Bfzcj   string `json:"bfzcj"` // 百分制成绩
	Bh      string `json:"bh"`    // 20230055
	Bh_id   string `json:"bh_id"`
	Bj      string `json:"bj"`
	Bzxx    string `json:"bzxx"`    // 备注信息
	Cjbz    string `json:"cjbz"`    // 成绩备注 缺考
	Czr     string `json:"czr"`     // cz人 王艳芳
	Cj      string `json:"cj"`      // 成绩
	Cjbdczr string `json:"cjbdczr"` // 陈爱华
	Cjbdsj  string `json:"cjbdsj"`  // 成绩bd时间？
	Cjsfzf  string `json:"cjsfzf"`  // 成绩是否作废

	Date               string `json:"date"`               // 二○二五年六月二十二日
	DateDigit          string `json:"dateDigit"`          // 2025年6月01日
	DateDigitSeparator string `json:"dateDigitSeparator"` // 2025-6-01

	Day    string `json:"day"`    // 22
	Jd     string `json:"jd"`     // 绩点 1.00
	Jg_id  string `json:"jg_id"`  // 003
	Jgmc   string `json:"jgmc"`   // 化学化工学院、应急管理与安全工程学院（合署）
	Jgpxzd string `json:"jgpxzd"` // 1
	Jsxm   string `json:"jsxm"`   // 教师姓名
	Jxb_id string `json:"jxb_id"` // 1C6308B42E124FA6E065FCFCFE1D0407
	Jxbmc  string `json:"jxbmc"`  // 教学班名称 有机化学A（下）-0002
	Kcbj   string `json:"kcbj"`   // 课程标记 主修

	Kch    string `json:"kch"`    // 课程号 0003021129
	Kch_id string `json:"kch_id"` // 课程号ID 0003021129
	Kclbmc string `json:"kclbmc"` // 课程类别 专业必修课
	Kcmc   string `json:"kcmc"`   // 课程名称 有机化学A（下）
	Kcxzdm string `json:"kcxzdm"` // 课程性质名称001
	Kcxzmc string `json:"kcxzmc"` // 课程性质名称 选修

	Key string `json:"key"` // =Jxb_id+"-"+Xh

	Ksxz   string `json:"ksxz"`   // 考试性质 正常考试、补考一、重修
	Kkbmmc string `json:"kkbmmc"` // 开课部门名称 开课学院	数理学院 马克思主义学院 人文社会科学学院
	Khfsmc string `json:"khfsmc"` // 考核方式 考查 考试
	Kklxdm string `json:"kklxdm"` // 板块课 主修课程 特殊课程 通识选修课

	Year    string `json:"year"`    //
	Rwzxs   string `json:"rwzxs"`   // 什么人数？ "40"
	Sfdkbcx string `json:"sfdkbcx"` // 否
	Sfxwkc  string `json:"sfxwkc"`  // 是否学位课程 否
	Sfzh    string `json:"sfzh"`    // 身份证号码
	Sfzx    string `json:"sfzx"`    // 是
	Tjrxm   string `json:"tjrxm"`   // 提交人姓名
	Tjsj    string `json:"tjsj"`    // 提交时间 2025-01-01 10:00:00
	Xb      string `json:"xb"`      // 性别 男
	Xbm     string `json:"xbm"`     // 性别码 男:1
	Xf      string `json:"xf"`      // 学分 2.5
	Xfjd    string `json:"xfjd"`    // 学分绩点 2.50
	Xh      string `json:"xh"`      // 学号
	Xh_id   string `json:"xh_id"`   // 001
	Xm      string `json:"xm"`      // 姓名
	Xnm     string `json:"xnm"`     // 2024
	Xnmmc   string `json:"xnmmc"`   // 2024-2025
	Xqm     string `json:"xqm"`     // 3
	Xqmmc   string `json:"xqmmc"`   // 1
	Zymc    string `json:"zymc"`    // 化学工程与工艺

	//Zymc string `json:"zymc"` // 化学工程与工艺
}
