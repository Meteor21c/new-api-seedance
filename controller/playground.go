package controller

import (
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func Playground(c *gin.Context) {
	var newAPIError *types.NewAPIError

	defer func() {
		if newAPIError != nil {
			c.JSON(newAPIError.StatusCode, gin.H{
				"error": newAPIError.ToOpenAIError(),
			})
		}
	}()

	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		newAPIError = types.NewError(errors.New("暂不支持使用 access token"), types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, nil, nil)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		return
	}

	userId := c.GetInt("id")

	// Write user context to ensure acceptUnsetRatio is available
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		return
	}
	userCache.WriteContext(c)

	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("playground-%s", relayInfo.UsingGroup),
		Group:  relayInfo.UsingGroup,
	}
	_ = middleware.SetupContextForToken(c, tempToken)

	Relay(c, types.RelayFormatOpenAI)
}

func PlaygroundVideo(c *gin.Context) {
	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		taskErr := &dto.TaskError{
			Code:       "access_denied",
			Message:    "暂不支持使用 access token",
			StatusCode: http.StatusForbidden,
			LocalError: true,
		}
		respondTaskError(c, taskErr)
		return
	}

	userId := c.GetInt("id")
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		respondTaskError(c, service.TaskErrorWrapperLocal(err, "query_user_failed", http.StatusInternalServerError))
		return
	}
	userCache.WriteContext(c)

	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("video-playground-%s", usingGroup),
		Group:  usingGroup,
	}
	if err := middleware.SetupContextForToken(c, tempToken); err != nil {
		respondTaskError(c, service.TaskErrorWrapperLocal(err, "setup_playground_token_failed", http.StatusInternalServerError))
		return
	}

	RelayTask(c)
}

func PlaygroundImage(c *gin.Context) {
	imageEditForm, err := validatePlaygroundImageEdit(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "invalid_request_error",
				"code":    "invalid_reference_image",
			},
		})
		return
	}
	if imageEditForm != nil {
		defer imageEditForm.RemoveAll()
	}

	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": "暂不支持使用 access token",
				"type":    "access_denied",
				"code":    "access_denied",
			},
		})
		return
	}

	userId := c.GetInt("id")
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "query_user_failed",
				"code":    "query_user_failed",
			},
		})
		return
	}
	userCache.WriteContext(c)

	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("image-playground-%s", usingGroup),
		Group:  usingGroup,
	}
	if err := middleware.SetupContextForToken(c, tempToken); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "setup_playground_token_failed",
				"code":    "setup_playground_token_failed",
			},
		})
		return
	}

	Relay(c, types.RelayFormatOpenAIImage)
}

func validatePlaygroundImageEdit(c *gin.Context) (*multipart.Form, error) {
	if !strings.HasPrefix(c.Request.URL.Path, "/pg/images/edits") {
		return nil, nil
	}

	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, fmt.Errorf("failed to parse reference images: %w", err)
	}

	var images []*multipart.FileHeader
	for field, files := range form.File {
		if field == "image" || field == "image[]" || strings.HasPrefix(field, "image[") {
			images = append(images, files...)
		}
	}
	if len(images) == 0 {
		return form, errors.New("at least one reference image is required")
	}
	if len(images) > 3 {
		return form, errors.New("no more than 3 reference images are allowed")
	}

	allowedExtensions := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".webp": true,
	}
	allowedTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
	}
	const maxReferenceImageSize = 10 * 1024 * 1024
	for _, image := range images {
		extension := strings.ToLower(filepath.Ext(image.Filename))
		contentType := strings.ToLower(strings.TrimSpace(image.Header.Get("Content-Type")))
		if !allowedExtensions[extension] || !allowedTypes[contentType] {
			return form, fmt.Errorf("reference image %q must be a static JPG, PNG, or WEBP file", image.Filename)
		}
		if image.Size <= 0 || image.Size > maxReferenceImageSize {
			return form, fmt.Errorf("reference image %q must be between 1 byte and 10 MiB", image.Filename)
		}
	}

	return form, nil
}
