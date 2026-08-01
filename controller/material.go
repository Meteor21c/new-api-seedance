package controller

import (
	"fmt"
	"net/http"
	"strconv"

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

// MaterialContent exposes a short-lived, signed material URL to upstream media
// providers. Authentication is carried by the opaque material_id in the path;
// the OSS bucket itself remains private.
func MaterialContent(c *gin.Context) {
	materialID := c.Param("material_id")
	fileName := c.Param("file_name")
	if materialID == "" || fileName == "" {
		c.Status(http.StatusNotFound)
		return
	}

	if c.Request.Method == http.MethodHead {
		metadata, err := service.StatMaterialObject(c.Request.Context(), materialID, fileName)
		if err != nil {
			common.SysLog("stat public material: " + err.Error())
			c.Status(http.StatusNotFound)
			return
		}
		writeMaterialHeaders(c, metadata)
		c.Status(http.StatusOK)
		return
	}

	object, err := service.OpenMaterialObject(c.Request.Context(), materialID, fileName)
	if err != nil {
		common.SysLog("open public material: " + err.Error())
		c.Status(http.StatusNotFound)
		return
	}
	defer object.Body.Close()

	writeMaterialHeaders(c, &object.MaterialObjectMetadata)
	c.DataFromReader(
		http.StatusOK,
		object.ContentLength,
		object.ContentType,
		object.Body,
		map[string]string{"Content-Disposition": fmt.Sprintf("inline; filename=%q", object.FileName)},
	)
}

func writeMaterialHeaders(c *gin.Context, metadata *service.MaterialObjectMetadata) {
	c.Header("Content-Type", metadata.ContentType)
	c.Header("Content-Length", strconv.FormatInt(metadata.ContentLength, 10))
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", metadata.FileName))
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
}
