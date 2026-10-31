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

// GetExam 查询考试安排。
// year: 学年，如 "2024"；term: 学期序号，即 TERM 的下标。
// 返回解析后的考试条目，打印/落盘由宿主负责。
func (a *APIClient) GetExam(ctx context.Context, year string, term int) ([]ExamItem, error) {
	if year == "" || term < 0 || term >= len(TERM) {
		return nil, fmt.Errorf("学年 / 学期不合法")
	}

	var exam Exam
	for i := 0; i < 3; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetTimeout(12 * time.Second).
			SetQueryParams(map[string]string{
				"gnmkdm": "N358105",
				"doType": "query",
			}).
			SetFormData(map[string]string{
				"xnm":                    year,
				"xqm":                    TERM[term],
				"_search":                "false",
				"ksmcdmb_id":             "",
				"kch":                    "",
				"kc":                     "",
				"ksrq":                   "",
				"kkbm_id":                "",
				"nd":                     strconv.FormatInt(time.Now().UnixMilli(), 10),
				"queryModel.showCount":   "15",
				"queryModel.currentPage": "1",
				"queryModel.sortName":    "",
				"queryModel.sortOrder":   "asc",
				"time":                   "1", // 查询次数
			}).
			Post(baseCfg.Exam)

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return nil, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("查询考试超时")
			}
			log.Println("考试请求错误:", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if resp.IsStatusFailure() {
			return nil, fmt.Errorf("考试查询失败: %s", resp.Status())
		}

		if !a.LoginCheck(resp) {
			return nil, ErrUnauthorized
		}

		if err := json.Unmarshal(resp.Bytes(), &exam); err != nil {
			return nil, fmt.Errorf("考试解析失败: %w", err)
		}
		return exam.Items, nil
	}
	return nil, fmt.Errorf("考试查询失败：重试次数已用尽")
}

// ExamItem 是一条考试安排。
type ExamItem struct {
	XhId        string `json:"xh_id"`
	Ksfs        string `json:"ksfs"`
	Bj          string `json:"bj"`
	Cdbh        string `json:"cdbh"`
	Ksmc        string `json:"ksmc"`
	Kssj        string `json:"kssj"`
	Kch         string `json:"kch"`
	Kkxy        string `json:"kkxy"`
	Cxbj        string `json:"cxbj"`
	Xqm         string `json:"xqm"`
	Khfs        string `json:"khfs"`
	Cdmc        string `json:"cdmc"`
	Cdxqmc      string `json:"cdxqmc"`
	Sksj        string `json:"sksj"`
	Kcmc        string `json:"kcmc"`
	Pycc        string `json:"pycc"`
	Totalresult int    `json:"totalresult"`
	Njmc        string `json:"njmc"`
	Jgmc        string `json:"jgmc"`
	Jxbmc       string `json:"jxbmc"`
	Sjbh        string `json:"sjbh"`
	Xb          string `json:"xb"`
	Zymc        string `json:"zymc"`
	Xf          string `json:"xf"`
	Xh          string `json:"xh"`
	Jxbzc       string `json:"jxbzc"`
	Xnmc        string `json:"xnmc"`
	Cdjc        string `json:"cdjc"`
	Xm          string `json:"xm"`
	Xnm         string `json:"xnm"`
	Xqmc        string `json:"xqmc"`
	Jsxx        string `json:"jsxx"`
	Xqmmc       string `json:"xqmmc"`
	RowId       int    `json:"row_id"`
	Zxbj        string `json:"zxbj"`
	Jxdd        string `json:"jxdd"`
}

type Exam struct {
	CurrentPage   int        `json:"currentPage"`
	CurrentResult int        `json:"currentResult"`
	EntityOrField bool       `json:"entityOrField"`
	Items         []ExamItem `json:"items"`
	Limit         int        `json:"limit"`
	Offset        int        `json:"offset"`
	PageNo        int        `json:"pageNo"`
	PageSize      int        `json:"pageSize"`
	ShowCount     int        `json:"showCount"`
	SortName      string     `json:"sortName"`
	SortOrder     string     `json:"sortOrder"`
	//Sorts       []interface{} `json:"sorts"`
	TotalCount  int `json:"totalCount"`
	TotalPage   int `json:"totalPage"`
	TotalResult int `json:"totalResult"`
}
