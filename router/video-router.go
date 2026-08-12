package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	// Public playback route authenticated by a short-lived task-bound token.
	// It exists for MCP/API clients that cannot attach Authorization headers to
	// a browser, media player, or download URL.
	signedVideoProxyRouter := router.Group("/v1")
	signedVideoProxyRouter.Use(middleware.RouteTag("relay"))
	signedVideoProxyRouter.Use(middleware.SystemPerformanceCheck())
	{
		signedVideoProxyRouter.GET("/videos/:task_id/content/:access_token", controller.SignedVideoProxy)
		signedVideoProxyRouter.HEAD("/videos/:task_id/content/:access_token", controller.SignedVideoProxy)
	}

	videoPlaygroundRouter := router.Group("/pg/video")
	videoPlaygroundRouter.Use(middleware.RouteTag("relay"))
	videoPlaygroundRouter.Use(middleware.SystemPerformanceCheck())
	videoPlaygroundRouter.Use(
		middleware.UserAuth(),
		middleware.SidebarModuleAuth("chat", "video"),
	)
	{
		videoPlaygroundRouter.POST("/generations", middleware.GenerationConcurrency("video"), middleware.Distribute(), controller.PlaygroundVideo)
		videoPlaygroundRouter.GET("/generations/:task_id", middleware.Distribute(), controller.RelayTaskFetch)
	}

	// Video proxy: accepts either session auth (dashboard) or token auth (API clients)
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"))
	videoProxyRouter.Use(middleware.TokenOrUserAuth())
	{
		videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)
		videoProxyRouter.HEAD("/videos/:task_id/content", controller.VideoProxy)
	}

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth())
	{
		videoV1Router.POST("/video/generations", middleware.GenerationConcurrency("video"), middleware.Distribute(), controller.RelayTask)
		videoV1Router.GET("/video/generations/:task_id", middleware.Distribute(), controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", middleware.GenerationConcurrency("video"), middleware.Distribute(), controller.RelayTask)
	}
	// openai compatible API video routes
	// docs: https://platform.openai.com/docs/api-reference/videos/create
	{
		videoV1Router.POST("/videos", middleware.GenerationConcurrency("video"), middleware.Distribute(), controller.RelayTask)
		videoV1Router.GET("/videos/:task_id", middleware.Distribute(), controller.RelayTaskFetch)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"))
	klingV1Router.Use(middleware.KlingRequestConvert(), middleware.TokenAuth())
	{
		klingV1Router.POST("/videos/text2video", middleware.GenerationConcurrency("video"), middleware.Distribute(), controller.RelayTask)
		klingV1Router.POST("/videos/image2video", middleware.GenerationConcurrency("video"), middleware.Distribute(), controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", middleware.Distribute(), controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", middleware.Distribute(), controller.RelayTaskFetch)
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"))
	jimengOfficialGroup.Use(middleware.JimengRequestConvert(), middleware.TokenAuth())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", middleware.GenerationConcurrency("video"), middleware.Distribute(), controller.RelayTask)
	}
}
