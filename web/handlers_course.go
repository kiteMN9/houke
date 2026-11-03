package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"school_sdk/protocol"

	"github.com/gin-gonic/gin"
)

func (s *Server) listTabs(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rt.engine.Tabs())
}

func (s *Server) selectTab(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "index 非法"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if err := rt.engine.SelectTab(ctx, idx); err != nil {
		writeEngineError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"modeName": rt.engine.ModeName(),
	})
}

// cachedCourses 只返回该账号内存里的课程缓存，不访问学校接口。
// 前端打开「课程浏览」时先用它还原界面；缓存为空时前端再调 listCourses
// 触发真实查询，避免每次进入页面都打一次学校接口。
func (s *Server) cachedCourses(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	list := rt.engine.Courses()
	if list == nil {
		list = []protocol.CustomCourseDic{}
	}
	c.JSON(http.StatusOK, list)
}

// listCourses 拉取最新课程列表并更新缓存，返回完整课程列表。
// ?force=1 时先清空已有缓存，避免残留已下架的课程。
func (s *Server) listCourses(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !rt.engine.Inited() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先调用 /init 完成初始化"})
		return
	}

	if c.Query("force") == "1" {
		rt.engine.ClearCourseCache()
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	list, err := rt.engine.GetCourseList(ctx)
	if err != nil {
		writeEngineError(c, err)
		return
	}
	rt.engine.CacheCourseList(list)
	c.JSON(http.StatusOK, rt.engine.Courses())
}

// courseDetail 拉取指定课程号下所有教学班详情并更新缓存，
// 返回完整课程列表（该课程号的条目会被填充 Do_jxb_id / 教师 / 上课时间等字段）。
func (s *Server) courseDetail(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if !rt.engine.Inited() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先调用 /init 完成初始化"})
		return
	}

	kchId := c.Param("kchId")
	if kchId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kchId 不能为空"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	detail, err := rt.engine.GetCourseDetail(ctx, kchId)
	if err != nil {
		writeEngineError(c, err)
		return
	}
	rt.engine.CacheCourseDetail(detail)
	c.JSON(http.StatusOK, rt.engine.Courses())
}

// clearCoursesCache 清空该账号的课程内存缓存。
func (s *Server) clearCoursesCache(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	rt.engine.ClearCourseCache()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type chooseReq struct {
	KchId string `json:"kchId"`
	JxbId string `json:"jxbId"`
	Xxkbj string `json:"xxkbj"`
	Cxbj  string `json:"cxbj"`
}

func (s *Server) choose(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	var req chooseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.KchId == "" || req.JxbId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kchId 和 jxbId 不能为空"})
		return
	}
	co := &protocol.CustomCourseDic{
		Kch_id:    req.KchId,
		Do_jxb_id: req.JxbId,
		Xxkbj:     req.Xxkbj,
		Cxbj:      req.Cxbj,
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := rt.engine.Choose(ctx, co)
	if err != nil {
		writeEngineError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

type quitReq struct {
	KchId string `json:"kchId"`
	JxbId string `json:"jxbId"`
}

func (s *Server) quit(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	var req quitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	ok, msg, err := rt.engine.Quit(ctx, req.JxbId, req.KchId)
	if err != nil {
		writeEngineError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": ok, "msg": msg})
}

func (s *Server) selected(c *gin.Context) {
	rt, err := s.store.runtime(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	// 未登录时直接失败：Session.Selected 会在内部触发一次阻塞登录（最长到账号的
	// 登录超时），这次登录不在任务面板里、也无法取消，前端按钮会一直转圈。
	if !rt.engine.LoggedIn() {
		writeUnauthorized(c, "尚未登录，请先登录")
		return
	}
	xn := c.Query("xnm")
	qm := c.Query("xqm")
	if xn == "" || qm == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 xnm / xqm 参数"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	list, err := rt.engine.Selected(ctx, xn, qm)
	if err != nil {
		writeEngineError(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}
