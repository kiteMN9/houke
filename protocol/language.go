package protocol

import (
	"fmt"
	"log"
	baseCfg "school_sdk/protocol/config"
	"time"
)

func (a *APIClient) SwitchLanguage(lang string) error {
	resp, err := a.Http.R().
		SetTimeout(12 * time.Second).
		SetFormData(map[string]string{
			"language": lang,
		}).
		Post(baseCfg.Language)
	if err != nil {
		fmt.Println(err)
		return err
	}
	if resp.IsStatusFailure() {
		log.Println("language HTTP 状态码错误:", resp.Status())
	}
	if resp.ResultError() != nil {
		log.Println(resp.ResultError(), resp.String())
	}
	if !a.LoginCheck(resp) {
		fmt.Println(resp.Status())
		return ErrUnauthorized
	}
	fmt.Println(resp.String())
	return nil
}
