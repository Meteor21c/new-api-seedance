package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetMaterialRouter(router *gin.Engine) {
	publicRouter := router.Group("/v1/materials")
	publicRouter.Use(middleware.RouteTag("relay"))
	publicRouter.Use(middleware.SystemPerformanceCheck())
	{
		publicRouter.GET("/content/:material_id/:file_name", controller.MaterialContent)
		publicRouter.HEAD("/content/:material_id/:file_name", controller.MaterialContent)
		publicRouter.OPTIONS("/content/:material_id/:file_name", controller.MaterialContent)
	}

	playgroundRouter := router.Group("/pg/materials")
	playgroundRouter.Use(middleware.RouteTag("relay"))
	playgroundRouter.Use(middleware.SystemPerformanceCheck())
	playgroundRouter.Use(
		middleware.UserAuth(),
		middleware.SidebarModuleAuth("chat", "video"),
	)
	{
		playgroundRouter.POST("/uploads", controller.CreateMaterialUpload)
	}

	apiRouter := router.Group("/v1/materials")
	apiRouter.Use(middleware.RouteTag("relay"))
	apiRouter.Use(middleware.SystemPerformanceCheck())
	apiRouter.Use(middleware.TokenAuth())
	{
		apiRouter.POST("/uploads", controller.CreateMaterialUpload)
	}
}
