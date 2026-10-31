package protocol

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"school_sdk/protocol/GPA"
	baseCfg "school_sdk/protocol/config"
)

// GetGPA 查询学业修读情况（含 GPA 与课程门数统计）。
// 返回结构化结果，打印/落盘由宿主负责。
func (a *APIClient) GetGPA(ctx context.Context) (GPA.Report, error) {
	if err := ctx.Err(); err != nil {
		return GPA.Report{}, err
	}

	resp, err := a.hedgeC.R().
		SetContext(ctx).
		SetRetryCount(0).
		SetQueryParam("gnmkdm", "N105515").
		SetResponseDoNotParse(true).
		Get(baseCfg.AcademiaIndex)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return GPA.Report{}, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return GPA.Report{}, fmt.Errorf("查询绩点超时")
		}
		log.Println("绩点请求错误:", err)
		return GPA.Report{}, fmt.Errorf("查询绩点失败: %w", err)
	}
	if resp.Body == nil {
		return GPA.Report{}, fmt.Errorf("查询绩点失败: 空响应")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode() == http.StatusFound {
		return GPA.Report{}, ErrUnauthorized
	}
	if resp.IsStatusFailure() {
		return GPA.Report{}, fmt.Errorf("查询绩点失败: %s", resp.Status())
	}

	report, err := GPA.Parse(resp.Body)
	if err != nil {
		return GPA.Report{}, err
	}
	return report, nil
}
