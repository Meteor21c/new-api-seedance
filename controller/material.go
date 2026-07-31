package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func CreateMaterialUpload(c *gin.Context) {
	var request service.MaterialUploadRequest
	if err := common.UnmarshalBodyReusable(c, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": "invalid material upload request"},
		})
		return
	}
	upload, err := service.CreateMaterialUpload(c.Request.Context(), c.GetInt("id"), request)
	if err != nil {
		status := http.StatusBadRequest
		if service.IsMaterialStorageDisabled(err) {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{
			"error": gin.H{"message": err.Error()},
		})
		return
	}
	c.JSON(http.StatusOK, upload)
}
