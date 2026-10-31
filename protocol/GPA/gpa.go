package GPA

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Report 是教务系统「学业修读情况」页的解析结果。
// 页面只给一段自然语言描述，所以数值字段用指针表示「页面上没找到」。
type Report struct {
	Name          string `json:"name"`
	StatsTime     string `json:"statsTime"`
	GPA           string `json:"gpa"`
	Total         *int   `json:"total,omitempty"`
	Passed        *int   `json:"passed,omitempty"`
	Failed        *int   `json:"failed,omitempty"`
	NotTaken      *int   `json:"notTaken,omitempty"`
	InProgress    *int   `json:"inProgress,omitempty"`
	OutsidePassed *int   `json:"outsidePassed,omitempty"`
	OutsideFailed *int   `json:"outsideFailed,omitempty"`
}

var (
	reName      = regexp.MustCompile(`([\p{Han}A-Za-z0-9·.]{1,20})\s*同学`)
	reStatsTime = regexp.MustCompile(`统计时间\s*(\d{4}-\d{1,2}-\d{1,2})(?:\s*(\d{1,2}:\d{1,2}:\d{1,2}))?`)
	reGPA       = regexp.MustCompile(`(?:平均学分绩点|GPA)[^0-9]{0,12}([0-9]+(?:\.[0-9]+)?)`)
	rePlan      = regexp.MustCompile(`计划总课程\s*(\d+)\s*门\s*通过\s*(\d+)\s*门\s*[，,]?\s*未通过\s*(\d+)\s*门\s*[；;]?\s*未修\s*(\d+)\s*门`)
	reProgress  = regexp.MustCompile(`在读\s*(\d+)\s*门`)
	reOutPassed = regexp.MustCompile(`计划外\s*[：:]?\s*通过\s*(\d+)\s*门`)
	reOutFailed = regexp.MustCompile(`未通过\s*(\d+)\s*门`)
)

// Parse 解析学业修读情况页，返回结构化数据。
func Parse(r io.Reader) (Report, error) {
	if r == nil {
		return Report{}, fmt.Errorf("响应为空")
	}
	text, err := alertText(r)
	if err != nil {
		return Report{}, err
	}
	if text == "" {
		return Report{}, fmt.Errorf("未找到学业修读信息（页面可能未登录或结构已变）")
	}
	return extractStats(text), nil
}

// extractStats 从 alertBox 的纯文本里按标签提取数字。
func extractStats(text string) Report {
	// 页面把每个词都包在标签里，text 里会残留大量空白，统一压成单空格。
	text = strings.Join(strings.Fields(text), " ")

	var r Report
	if m := reName.FindStringSubmatch(text); len(m) > 1 {
		r.Name = m[1]
	}
	if m := reStatsTime.FindStringSubmatch(text); len(m) > 1 {
		r.StatsTime = m[1]
		if m[2] != "" {
			r.StatsTime += " " + m[2]
		}
	}
	if m := reGPA.FindStringSubmatch(text); len(m) > 1 {
		r.GPA = m[1]
	}
	if m := rePlan.FindStringSubmatch(text); len(m) > 1 {
		r.Total = atoi(m[1])
		r.Passed = atoi(m[2])
		r.Failed = atoi(m[3])
		r.NotTaken = atoi(m[4])
	}
	if m := reProgress.FindStringSubmatch(text); len(m) > 1 {
		r.InProgress = atoi(m[1])
	}
	if m := reOutPassed.FindStringSubmatch(text); len(m) > 1 {
		r.OutsidePassed = atoi(m[1])
	}
	// 「计划外」之前已经取过「未通过」，只在后半段找计划外的未通过。
	if idx := strings.Index(text, "计划外"); idx >= 0 {
		if m := reOutFailed.FindStringSubmatch(text[idx:]); len(m) > 1 {
			r.OutsideFailed = atoi(m[1])
		}
	}
	return r
}

func atoi(s string) *int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return &n
}

// alertText 用 tokenizer 取 #alertBox 里的文本，避免把整页 HTML 当字符串处理。
func alertText(r io.Reader) (string, error) {
	tokenizer := html.NewTokenizer(io.LimitReader(r, 40000))
	var inAlert bool
	depth := 0
	var b strings.Builder

	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				return "", err
			}
			break
		}
		switch tt {
		case html.StartTagToken:
			token := tokenizer.Token()
			if token.Data != "div" {
				continue
			}
			if inAlert {
				depth++
				continue
			}
			for _, attr := range token.Attr {
				if attr.Key == "id" && attr.Val == "alertBox" {
					inAlert = true
					depth = 1
					break
				}
			}
		case html.EndTagToken:
			if !inAlert || tokenizer.Token().Data != "div" {
				continue
			}
			depth--
			if depth == 0 {
				return b.String(), nil
			}
		case html.TextToken:
			if inAlert {
				b.WriteString(string(tokenizer.Text()))
			}
		}
	}
	if !inAlert {
		return "", nil
	}
	return b.String(), nil
}
