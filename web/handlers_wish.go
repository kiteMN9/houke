package web

import (
	"net/http"

	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

// wishView 是愿望清单的 API 形态，也是 WebUI 里"高级选课"表单的三个输入框。
// 三个字段都可以为空；全空表示没有配置愿望清单，此时 /auto-choose 会拒绝执行。
type wishView struct {
	WantClasses  []string `json:"wantClasses"`
	WantTeachers []string `json:"wantTeachers"`
	WantTypes    []string `json:"wantTypes"`
}

func wishViewOf(a *Account) wishView {
	return wishView{
		WantClasses:  a.WantClasses,
		WantTeachers: a.WantTeachers,
		WantTypes:    a.WantTypes,
	}
}

// getWish 返回账号已保存的愿望清单。
func (s *Server) getWish(c *gin.Context) {
	a, ok := s.store.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}
	c.JSON(http.StatusOK, wishViewOf(a))
}

// updateWish 全量替换愿望清单：传空数组就是清空。
//
// 单独一个端点而不是并进 PUT /accounts：账号更新的语义是"空字符串=不修改"，
// 数组没有等价的哨兵值，混在一起会让"清空"和"没提交"分不清。
func (s *Server) updateWish(c *gin.Context) {
	id := c.Param("id")
	var req wishView
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	acc, err := s.store.SetWish(id, protocol.WishList{
		WantClassList:   req.WantClasses,
		WantTeacherList: req.WantTeachers,
		WantTypeList:    req.WantTypes,
	})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, wishViewOf(acc))
}
