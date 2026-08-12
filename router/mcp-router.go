package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetMCPRouter(router *gin.Engine) {
	controller.SetMCPInternalHandler(router)

	mcpRouter := router.Group("/mcp")
	mcpRouter.Use(middleware.RouteTag("relay"))
	mcpRouter.Use(middleware.SystemPerformanceCheck())
	mcpRouter.Use(middleware.TokenAuth())
	{
		mcpRouter.POST("", controller.MCP)
		mcpRouter.POST("/image", controller.MCPImage)
		mcpRouter.POST("/video", controller.MCPVideo)
		mcpRouter.GET("", func(c *gin.Context) {
			c.Status(http.StatusMethodNotAllowed)
		})
		mcpRouter.GET("/image", func(c *gin.Context) {
			c.Status(http.StatusMethodNotAllowed)
		})
		mcpRouter.GET("/video", func(c *gin.Context) {
			c.Status(http.StatusMethodNotAllowed)
		})
		mcpRouter.DELETE("", func(c *gin.Context) {
			c.Status(http.StatusMethodNotAllowed)
		})
		mcpRouter.DELETE("/image", func(c *gin.Context) {
			c.Status(http.StatusMethodNotAllowed)
		})
		mcpRouter.DELETE("/video", func(c *gin.Context) {
			c.Status(http.StatusMethodNotAllowed)
		})
	}
}
