package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// shutdownTimeout 优雅退出的等待上限。等太久用户会以为又"退不掉"，
// 也要给 Windows 关闭控制台窗口时的系统宽限留出余量（系统在 SIGTERM
// 处理者不返回时会于数秒后强制结束进程）；超过这里限制的连接会被强制关闭。
const shutdownTimeout = 3 * time.Second

//go:embed static/*
var staticFS embed.FS

type Server struct {
	store  *AccountStore
	tasks  *taskStore
	router *gin.Engine

	// 进程级上下文：Shutdown 时先取消它。请求的 context 由它派生
	//（http.Server.BaseContext），所以 SSE 这类长连接会随退出一起结束——
	// http.Server.Shutdown 只等连接空闲，并不会取消已建立的请求上下文。
	baseCtx    context.Context
	baseCancel context.CancelFunc

	// 真正的 HTTP 服务实例。gin 的 Engine.Run 内部自己 new 一个
	// http.Server 并且不给关停的机会，这里自己持有，才能优雅退出。
	httpServer *http.Server

	// 服务端配置（server.json）：目前是 SMTP 邮件通知，和账号无关。
	serverCfg *serverSettingsManager

	// 微信扫码登录（不经过账号）：同一时刻只允许一次。
	// wechatMu 保护指针本身，wechatFlow 自己保护结果字段。
	wechatMu sync.Mutex
	wechat   *wechatFlow
}

func NewServer(store *AccountStore) (*Server, error) {
	//gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	err := r.SetTrustedProxies([]string{"127.0.0.1"})
	if err != nil {
		return nil, err
	}

	// 服务端配置与 accounts.json 同目录；第一次启动时尝试导入旧版 SMTP.json。
	cfgMgr, cfgErr := newServerSettingsManager(
		filepath.Join(filepath.Dir(store.filename), "server.json"),
		filepath.Join(filepath.Dir(store.filename), "SMTP.json"),
	)
	if cfgErr != nil {
		if cfgMgr == nil {
			return nil, fmt.Errorf("加载服务端配置失败: %w", cfgErr)
		}
		// 配置损坏不阻止启动：页面还能重新保存一份合法配置。
		log.Printf("服务端配置有问题，暂用默认设置: %v", cfgErr)
	}

	s := &Server{store: store, tasks: store.tasks, router: r, serverCfg: cfgMgr}
	s.baseCtx, s.baseCancel = context.WithCancel(context.Background())
	s.httpServer = &http.Server{
		Handler: r,
		BaseContext: func(net.Listener) context.Context {
			return s.baseCtx
		},
	}
	s.registerRoutes()
	if err := s.registerStatic(); err != nil {
		return nil, fmt.Errorf("挂载静态资源失败: %w", err)
	}
	return s, nil
}

// Listen 绑定监听地址。先绑定、后打印启动信息，端口被占用时就不会出现
// 「WebUI 已启动」的假成功（旧实现的 Run 先打印再 ListenAndServe）。
func (s *Server) Listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// Serve 在 ln 上提供服务，直到 ctx 结束（优雅退出）或服务本身出错。
//
// ctx 结束时按顺序收尾（见 Shutdown）：取消运行中的长任务 → 取消请求
// 上下文（SSE 长连接随之结束）→ 关闭 listener 并等在途请求结束。超过
// shutdownTimeout 仍在的连接会被强制关闭，保证进程一定退得掉。
// 正常关停返回 nil；Serve 因关闭而返回的 http.ErrServerClosed 不是错误。
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpServer.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Printf("收到退出信号，开始优雅退出")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			// 用户是主动退出，超时不算运行错误：打印提示、强制关干净即可。
			log.Printf("优雅退出超时（%s），强制关闭剩余连接: %v", shutdownTimeout, err)
		}
		// 等 Serve 协程返回，避免关停后仍有 goroutine 写已关闭的 listener。
		<-errCh
		return nil
	}
}

// Shutdown 优雅关闭 WebUI，可重复调用。顺序是刻意安排的：
//
//  1. 取消仍在运行的登录 / 初始化 / 自动选课任务。这些任务的 context 都
//     派生自 context.Background()（见 handlers_login.go 等），不主动取消
//     就会继续跑到自己的预算——自动选课默认 24 小时，进程退了任务还在发请求。
//  2. 取消所有请求的 context：SSE（/api/accounts/:id/events）挂在请求 ctx
//     上，http.Server.Shutdown 不会替我们取消它，不先取消就会一直等到超时。
//  3. 关闭 listener、拒绝新连接，等在途请求结束；超时则强制 Close 兜底。
func (s *Server) Shutdown(ctx context.Context) error {
	n := s.tasks.cancelAll()
	s.baseCancel()
	err := s.httpServer.Shutdown(ctx)
	if err != nil {
		_ = s.httpServer.Close()
	}
	log.Printf("WebUI 已停止（取消 %d 个运行中的任务）", n)
	return err
}

func (s *Server) registerStatic() error {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("static 目录不存在: %w", err)
	}
	s.router.StaticFS("/ui", http.FS(sub))
	s.router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/ui/")
	})
	return nil
}

func (s *Server) registerRoutes() {
	// 简单的健康检查
	s.router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	api := s.router.Group("/api")
	{
		// 账号管理
		api.GET("/accounts", s.listAccounts)         // 账号列表
		api.POST("/accounts", s.createAccount)       // 新建账号（body: name,baseUrl,username,password,casPassword,casLogin,timeout,existVerify,userAgent）
		api.GET("/accounts/:id", s.getAccount)       // 账号详情
		api.PUT("/accounts/:id", s.updateAccount)    // 更新
		api.DELETE("/accounts/:id", s.deleteAccount) // 删除账号
		// 会话失效时是否自动重登（热切换，不重建运行态）
		api.PUT("/accounts/:id/auto-relogin", s.setAutoRelogin)
		// 自动选课结束时是否给该账号发结果邮件（热切换，不重建运行态）
		api.PUT("/accounts/:id/auto-notify", s.setAutoNotify)

		// 愿望清单（高级选课的持久化配置）：GET 读取、PUT 全量替换
		api.GET("/accounts/:id/wish", s.getWish)
		api.PUT("/accounts/:id/wish", s.updateWish)

		// 登录/状态
		api.POST("/accounts/:id/login", s.login)                  // 登录（最长阻塞「登录超时」，默认 10 分钟，可被 /login/cancel 取消）
		api.POST("/accounts/:id/login/cancel", s.cancelLogin)     // 取消登录
		api.GET("/accounts/:id/login/pending", s.pendingInput)    // 查询是否有待输入的验证码/密码
		api.POST("/accounts/:id/login/input", s.submitLoginInput) // 提交验证码 / 新密码
		api.GET("/accounts/:id/login/qr", s.loginQR)              // 微信扫码登录的二维码图片（rev 可选）
		api.POST("/accounts/:id/cas/jwt/clear", s.clearCasJWT)    // 清空该账号的 JWT
		api.POST("/accounts/:id/logout", s.logout)                // 登出（清空会话）
		api.GET("/accounts/:id/status", s.status)                 // 登录/初始化状态（随时可查）
		api.GET("/accounts/:id/events", s.taskEvents)             // 状态推送（SSE），替代高频轮询 /status

		// 初始化选课
		api.POST("/accounts/:id/init", s.initCourse)              // 初始化（抓取选课参数，最长阻塞「登录超时」，可被 /init/cancel 取消）
		api.POST("/accounts/:id/init/cancel", s.cancelInitCourse) // 取消初始化

		// 页签
		api.GET("/accounts/:id/tabs", s.listTabs)          // 可用页签
		api.POST("/accounts/:id/tabs/:index", s.selectTab) // 切换页签

		// 课程
		api.GET("/accounts/:id/courses", s.listCourses)          // 刷新课程列表 → 返回完整课程列表
		api.GET("/accounts/:id/courses/cache", s.cachedCourses)  // 只读内存缓存 → 不访问学校接口
		api.GET("/accounts/:id/courses/:kchId", s.courseDetail)  // 刷新指定课程号详情 → 返回完整课程列表
		api.DELETE("/accounts/:id/courses", s.clearCoursesCache) // 清空课程缓存
		api.POST("/accounts/:id/choose", s.choose)               // 选课（body: kchId,jxbId）
		api.POST("/accounts/:id/quit", s.quit)                   // 退课（body: kchId,jxbId）
		api.GET("/accounts/:id/selected", s.selected)            // 已选课程

		// 自动选课（高级选课）：按愿望清单反复尝试，进度与结果走任务事件流
		api.POST("/accounts/:id/auto-choose", s.autoChoose)              // body: wantClasses,wantTeachers,wantTypes,intervalSeconds,maxRounds,once
		api.POST("/accounts/:id/auto-choose/cancel", s.cancelAutoChoose) // 取消自动选课

		// 成绩
		api.GET("/accounts/:id/score/suggest", s.scoreSuggest) // 建议学年学期
		api.GET("/accounts/:id/score", s.getScore)             // 成绩查询

		// 其他工具（个人资料 / 大头照 / Cookie / 会话诊断）
		api.GET("/accounts/:id/tools/info", s.toolsInfo)       // 个人信息（需登录）
		api.GET("/accounts/:id/tools/photo", s.toolsPhoto)     // 学生大头照（需登录）
		api.GET("/accounts/:id/tools/cookies", s.toolsCookies) // 当前 Cookie（不要求登录）
		api.GET("/accounts/:id/tools/debug", s.toolsDebug)     // 会话诊断（不要求登录，不含密码）

		api.GET("/accounts/:id/ping", s.ping) // 探测会话（不重新登录）

		// 服务端配置（全局，不依赖账号）：目前是 SMTP 邮件通知
		api.GET("/server", s.getServerSettings)    // 配置 + 配置是否完整（不测试连接）
		api.PUT("/server", s.updateServerSettings) // 保存 SMTP 配置
		api.POST("/server/smtp/test", s.testSMTP)  // 用当前/已保存配置发测试邮件

		// 字符验证码模型配置（全局，不依赖账号）
		api.GET("/kaptcha", s.kaptchaStatus)             // 配置 + 模型加载状态
		api.PUT("/kaptcha", s.updateKaptcha)             // 保存并热加载
		api.POST("/kaptcha/reload", s.reloadKaptcha)     // 重新读取模型
		api.POST("/kaptcha/model", s.uploadKaptchaModel) // 上传 .onnx 模型
		api.POST("/kaptcha/test", s.testKaptcha)         // 测试识别一张图

		// 微信扫码登录（不经过账号）
		//
		// 扫码之前没有账号，也没有任何要填的东西：点一下就开始，二维码出来后
		// 用微信扫。登录成功的回报不是"某个账号登录了"，而是一张 ticket——
		// 从 ticket 里才知道是谁，再决定绑到哪个账号（没有就新建）。
		api.POST("/wechat/login", s.wechatLogin)   // 发起扫码（body 为空）
		api.GET("/wechat/status", s.wechatStatus)  // 轮询：二维码 / 登录阶段 / 绑定结果
		api.GET("/wechat/qr", s.wechatQR)          // 二维码图片（rev 可选）
		api.POST("/wechat/cancel", s.wechatCancel) // 取消整次扫码
	}
}
