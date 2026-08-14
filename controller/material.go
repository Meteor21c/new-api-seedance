package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

var (
	presignGeneratedImageObject = service.PresignGeneratedImageObject
	openMaterialObject          = service.OpenMaterialObject
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
	writeMaterialCORSHeaders(c)
	if c.Request.Method == http.MethodOptions {
		c.Status(http.StatusNoContent)
		return
	}
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

	// Generated results are read by browsers and Codex clients, not by image
	// providers that require signature validation through this process. Redirect
	// them to OSS so application CPU, memory, bandwidth, and open connections
	// stay low. If presigning is temporarily unavailable, retain the reliable
	// streaming fallback below.
	if redirect, err := presignGeneratedImageObject(c.Request.Context(), materialID, fileName); err == nil && redirect != nil && redirect.URL != "" {
		c.Header("Cache-Control", "private, no-store")
		c.Redirect(http.StatusFound, redirect.URL)
		return
	}

	object, err := openMaterialObject(c.Request.Context(), materialID, fileName)
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

func writeMaterialCORSHeaders(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	c.Header("Access-Control-Allow-Headers", "Range, Content-Type")
	c.Header("Access-Control-Expose-Headers", "Content-Length, Content-Type, Content-Disposition")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	c.Header("Referrer-Policy", "no-referrer")
}

func writeMaterialHeaders(c *gin.Context, metadata *service.MaterialObjectMetadata) {
	c.Header("Content-Type", metadata.ContentType)
	c.Header("Content-Length", strconv.FormatInt(metadata.ContentLength, 10))
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", metadata.FileName))
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
}
