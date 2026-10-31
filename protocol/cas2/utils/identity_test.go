package utils

import (
	"encoding/base64"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeJWT 把 claims 编成 "header.payload.sig"；解析不验签，签名随便填。
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err, "marshal claims")
	return "e30." + base64.RawStdEncoding.EncodeToString(payload) + ".sig"
}

// 扫码登录的全部依据都在这张票里：外层给学号 + 学校编码，内层给姓名。
func TestParseTicketIdentity(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	inner := makeJWT(t, map[string]any{
		"ATTR_userNo":           "20230001",
		"ATTR_name":             "张三",
		"ATTR_identityTypeName": "学生",
		"ATTR_organizationName": "盐城工学院",
	})
	outer := makeJWT(t, map[string]any{
		"sub":              "20230001",
		"organizationCode": "00300",
		"idToken":          inner,
		"exp":              exp.Unix(),
	})

	tk, err := ParseTicket(outer)
	require.NoError(t, err)
	assert.Equal(t, "20230001", tk.Identity.Sub)
	assert.Equal(t, "张三", tk.Identity.Name)
	assert.Equal(t, "学生", tk.Identity.Type)
	assert.Equal(t, "盐城工学院", tk.Identity.Org)
	assert.Equal(t, "00300", tk.Identity.OrgCode)
	assert.Equal(t, inner, tk.IDToken)
	assert.Equal(t, exp.Unix(), tk.Exp.Unix())
}

// 没有内层票据时身份仍然可用：学号在外层，姓名只是展示。
func TestParseTicketWithoutIDToken(t *testing.T) {
	outer := makeJWT(t, map[string]any{"sub": "20230001", "organizationCode": "00300"})

	tk, err := ParseTicket(outer)
	require.NoError(t, err)
	assert.False(t, tk.Identity.Empty())
	assert.Empty(t, tk.Identity.Name)
	assert.Equal(t, "00300", tk.Identity.OrgCode)
	assert.True(t, tk.Exp.IsZero())
}

// 没有 sub 的票据不能拿来绑定账号，必须报错而不是绑到空身份上。
func TestParseTicketRejectsMissingSubject(t *testing.T) {
	outer := makeJWT(t, map[string]any{"organizationCode": "00300"})

	_, err := ParseTicket(outer)
	require.Error(t, err)
}

// 内层票据解不开时不能连累整个登录：学号已经有了，姓名缺了就不显示。
func TestParseTicketToleratesBrokenIDToken(t *testing.T) {
	outer := makeJWT(t, map[string]any{
		"sub":     "20230001",
		"idToken": "not-a-jwt",
	})

	tk, err := ParseTicket(outer)
	require.NoError(t, err)
	assert.Equal(t, "20230001", tk.Identity.Sub)
	assert.Empty(t, tk.Identity.Name)
}
