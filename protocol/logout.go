package protocol

import (
	"context"
	"errors"
	"fmt"
	baseCfg "school_sdk/protocol/config"
	"school_sdk/utils"
	"strconv"
	"time"
)

func (a *APIClient) Logout() string {
	// 本次调用的语义就是"退出登录"：请求是否顺利都不该让本地继续显示已登录，
	// 所以收尾时统一把登录态置为 false（要重新登录请调用 Login）。
	defer a.setLoggedIn(false)

	// 超时强制退出机制
	resp, err := a.Http.R().
		SetRetryCount(0).
		SetTimeout(time.Second * 2).
		SetQueryParams(map[string]string{
			"t":          strconv.FormatInt(time.Now().UnixMilli(), 10),
			"login_type": "",
		}).
		Get(baseCfg.LOGOUT)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ""
		}

		fmt.Println(err)
	}

	// fmt.Println(resp.String()
	bodyString := resp.String()
	if !a.CheckLogout302(resp) && utils.UserIsLogin(a.cfg.Account, bodyString) {
		fmt.Println("退出失败")
	} else {
		fmt.Println("退出成功")
	}

	return bodyString

}
