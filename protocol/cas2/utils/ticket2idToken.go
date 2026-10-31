package utils

import (
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"log"
	"strings"
	"time"

	"school_sdk/protocol/config"
)

// JWTContent 是 CAS ticket（外层 JWT）的 payload。
type JWTContent struct {
	IdentityTypeCode string `json:"identityTypeCode"`
	Aud              string `json:"aud"` // Audience
	Sub              string `json:"sub"` // Subject Account
	OrganizationCode string `json:"organizationCode"`
	Iss              string `json:"iss"` // Issuer
	IdToken          string `json:"idToken"`
	Exp              int64  `json:"exp"` // Expiration Time
	Iat              int64  `json:"iat"` // Issued At
	Jti              string `json:"jti"` // JWT ID
}

// idTokenClaims 是内层 idToken 里我们关心的字段。
//
// 内层票据是门户自己签的，明文放着用户属性；外层 JWT 只有 sub，
// 姓名、学校这些展示信息得从内层拿。
type idTokenClaims struct {
	UserNo           string `json:"ATTR_userNo"`
	Name             string `json:"ATTR_name"`
	IdentityTypeName string `json:"ATTR_identityTypeName"`
	OrganizationName string `json:"ATTR_organizationName"`
}

// Ticket 是一张 CAS ticket 解析出来的全部登录信息。
type Ticket struct {
	// IDToken 是内层票据，后续请求要放在 x-id-token 头里。
	IDToken string
	// Exp 是外层 JWT 的过期时间；零值表示票据没带 exp。
	Exp time.Time
	// Identity 是 ticket 声明的身份。Sub 一定非空。
	Identity config.Identity
}

// ParseTicket 把 ticket 解析成票据内容 + 身份。
//
// 扫完码才知道登的是谁，就靠这一步：外层 JWT 给出 sub 和学校编码，
// 内层 idToken 补上姓名和学校名。
func ParseTicket(tokenString string) (Ticket, error) {
	var outer JWTContent
	if err := decodeJWTPayload(tokenString, &outer); err != nil {
		return Ticket{}, err
	}

	id := config.Identity{Sub: outer.Sub, OrgCode: outer.OrganizationCode}
	if outer.IdToken != "" {
		var inner idTokenClaims
		if err := decodeJWTPayload(outer.IdToken, &inner); err != nil {
			// 内层解不开不影响"这是谁"：学号在外层 sub 里已经有了，
			// 姓名 / 学校只是展示信息，缺了就回落显示学号。
			log.Printf("idToken 解析失败（仍按 sub 登录）: %v\n", err)
		} else {
			id.Name = inner.Name
			id.Type = inner.IdentityTypeName
			id.Org = inner.OrganizationName
			if id.Sub == "" {
				id.Sub = inner.UserNo
			}
		}
	}
	if id.Sub == "" {
		return Ticket{}, fmt.Errorf("ticket 里没有身份标识（sub 为空）")
	}

	var expTime time.Time
	if outer.Exp != 0 {
		expTime = time.Unix(outer.Exp, 0)
		// 创建东八区时区
		east8Zone := time.FixedZone("CST", 8*60*60)
		log.Printf("Token expires at: %s\n", expTime.In(east8Zone).Format(time.RFC3339))
	}

	return Ticket{IDToken: outer.IdToken, Exp: expTime, Identity: id}, nil
}

// ExtractIDToken idToken, expTime, Account
//
// 兼容入口：只关心"票 + 过期时间 + 学号"的调用方用它，需要身份全貌用 ParseTicket。
func ExtractIDToken(tokenString string) (string, time.Time, string, error) {
	ticket, err := ParseTicket(tokenString)
	if err != nil {
		return "", time.Time{}, "", err
	}
	return ticket.IDToken, ticket.Exp, ticket.Identity.Sub, nil
}

// decodeJWTPayload 解出 JWT 的 payload 部分（不校验签名：票据来源是
// 门户自己 302 回来的，验签既没有公钥也没有必要）。
func decodeJWTPayload(tokenString string, v any) error {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return fmt.Errorf("无效的token格式，期望3部分，实际得到%d部分", len(parts))
	}
	payload := parts[1]
	// 添加 padding 如果必要（base64 解码需要正确的 padding）
	if len(payload)%4 != 0 {
		payload += strings.Repeat("=", 4-len(payload)%4)
	}
	decodedBytes, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		// 如果 RawStdEncoding 失败，尝试标准编码
		decodedBytes, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return fmt.Errorf("Base64解码失败: %w", err)
		}
	}
	if err := json.Unmarshal(decodedBytes, v); err != nil {
		return fmt.Errorf("JSON解析失败: %w", err)
	}
	return nil
}
