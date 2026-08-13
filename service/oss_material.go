package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/google/uuid"
)

const (
	MaterialObjectKeysContextKey = "oss_material_object_keys"

	defaultMaterialPrefix       = "temp-materials/"
	defaultMaterialMaxFileSize  = int64(10 * 1024 * 1024)
	defaultMaterialUploadTTL    = 10 * time.Minute
	defaultMaterialDownloadTTL  = 24 * time.Hour
	defaultMaterialTokenTTL     = 24 * time.Hour
	defaultMaterialCleanupDelay = time.Hour
)

var (
	errMaterialStorageDisabled = errors.New("temporary material storage is not configured")
	materialClientMu           sync.Mutex
	materialClient             *oss.Client
	materialClientConfigKey    string
)

type MaterialUploadRequest struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type MaterialUpload struct {
	MaterialID string            `json:"material_id"`
	UploadURL  string            `json:"upload_url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers"`
	ExpiresAt  int64             `json:"expires_at"`
}

type MaterialObjectMetadata struct {
	ContentType   string
	ContentLength int64
	FileName      string
}

type MaterialObject struct {
	MaterialObjectMetadata
	Body io.ReadCloser
}

type GeneratedImageAsset struct {
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
	MimeType  string `json:"mime_type"`
}

type GeneratedImageRedirect struct {
	URL string
	MaterialObjectMetadata
}

type materialToken struct {
	Version     int    `json:"v"`
	UserID      int    `json:"user_id"`
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	ExpiresAt   int64  `json:"expires_at"`
}

type materialStorageConfig struct {
	Region        string
	Endpoint      string
	Bucket        string
	Prefix        string
	RoleName      string
	PublicBaseURL string
	MaxFileSize   int64
	UploadTTL     time.Duration
	DownloadTTL   time.Duration
	TokenTTL      time.Duration
	CleanupDelay  time.Duration
}

func CreateMaterialUpload(ctx context.Context, userID int, request MaterialUploadRequest) (*MaterialUpload, error) {
	if userID <= 0 {
		return nil, errors.New("authenticated user is required")
	}
	cfg, err := loadMaterialStorageConfig()
	if err != nil {
		return nil, err
	}
	contentType, extension, err := normalizeMaterialType(request.FileName, request.ContentType)
	if err != nil {
		return nil, err
	}
	if request.SizeBytes <= 0 {
		return nil, errors.New("size_bytes must be greater than zero")
	}
	if request.SizeBytes > cfg.MaxFileSize {
		return nil, fmt.Errorf("file exceeds the %d byte upload limit", cfg.MaxFileSize)
	}

	client, err := getMaterialClient(cfg)
	if err != nil {
		return nil, err
	}
	objectKey := cfg.Prefix + strconv.Itoa(userID) + "/" + uuid.NewString() + extension
	putRequest := &oss.PutObjectRequest{
		Bucket:          oss.Ptr(cfg.Bucket),
		Key:             oss.Ptr(objectKey),
		ContentLength:   oss.Ptr(request.SizeBytes),
		ContentType:     oss.Ptr(contentType),
		ForbidOverwrite: oss.Ptr("true"),
	}
	result, err := client.Presign(ctx, putRequest, oss.PresignExpires(cfg.UploadTTL))
	if err != nil {
		return nil, fmt.Errorf("create OSS upload signature: %w", err)
	}

	token, err := encodeMaterialToken(materialToken{
		Version:     1,
		UserID:      userID,
		ObjectKey:   objectKey,
		ContentType: contentType,
		SizeBytes:   request.SizeBytes,
		ExpiresAt:   time.Now().Add(cfg.TokenTTL).Unix(),
	})
	if err != nil {
		return nil, err
	}
	return &MaterialUpload{
		MaterialID: token,
		UploadURL:  result.URL,
		Method:     result.Method,
		Headers:    result.SignedHeaders,
		ExpiresAt:  result.Expiration.Unix(),
	}, nil
}

func IsMaterialStorageDisabled(err error) bool {
	return errors.Is(err, errMaterialStorageDisabled)
}

func ResolveMaterialIDs(ctx context.Context, userID int, materialIDs []string) ([]string, []string, error) {
	if len(materialIDs) == 0 {
		return nil, nil, nil
	}
	cfg, err := loadMaterialStorageConfig()
	if err != nil {
		return nil, nil, err
	}
	client, err := getMaterialClient(cfg)
	if err != nil {
		return nil, nil, err
	}

	urls := make([]string, 0, len(materialIDs))
	objectKeys := make([]string, 0, len(materialIDs))
	seen := make(map[string]struct{}, len(materialIDs))
	for _, rawID := range materialIDs {
		token, err := decodeMaterialToken(strings.TrimSpace(rawID))
		if err != nil {
			return nil, nil, err
		}
		if token.UserID != userID {
			return nil, nil, errors.New("material does not belong to the authenticated user")
		}
		if token.ExpiresAt < time.Now().Unix() {
			return nil, nil, errors.New("material_id has expired")
		}
		if !strings.HasPrefix(token.ObjectKey, cfg.Prefix+strconv.Itoa(userID)+"/") {
			return nil, nil, errors.New("material object path is invalid")
		}
		if _, exists := seen[token.ObjectKey]; exists {
			continue
		}

		head, err := client.HeadObject(ctx, &oss.HeadObjectRequest{
			Bucket: oss.Ptr(cfg.Bucket),
			Key:    oss.Ptr(token.ObjectKey),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("material upload is unavailable: %w", err)
		}
		if head.ContentLength > cfg.MaxFileSize {
			return nil, nil, errors.New("uploaded material exceeds the configured file size limit")
		}
		if _, metadataErr := validateMaterialMetadata(token, head.ContentLength, materialString(head.ContentType)); metadataErr != nil {
			return nil, nil, metadataErr
		}

		materialURL, ok := buildMaterialPublicURL(cfg.PublicBaseURL, strings.TrimSpace(rawID), token.ContentType)
		if !ok {
			result, presignErr := client.Presign(ctx, &oss.GetObjectRequest{
				Bucket: oss.Ptr(cfg.Bucket),
				Key:    oss.Ptr(token.ObjectKey),
			}, oss.PresignExpires(cfg.DownloadTTL))
			if presignErr != nil {
				return nil, nil, fmt.Errorf("create OSS download signature: %w", presignErr)
			}
			materialURL = result.URL
		}
		urls = append(urls, materialURL)
		objectKeys = append(objectKeys, token.ObjectKey)
		seen[token.ObjectKey] = struct{}{}
	}
	return urls, objectKeys, nil
}

// StatMaterialObject validates a public material token and returns metadata
// without exposing OSS credentials. It is used by the unauthenticated HEAD
// endpoint that media providers call before downloading a reference image.
func StatMaterialObject(ctx context.Context, materialID, requestedFileName string) (*MaterialObjectMetadata, error) {
	cfg, token, client, err := prepareMaterialAccess(materialID, requestedFileName)
	if err != nil {
		return nil, err
	}
	result, err := client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: oss.Ptr(cfg.Bucket),
		Key:    oss.Ptr(token.ObjectKey),
	})
	if err != nil {
		return nil, fmt.Errorf("material is unavailable: %w", err)
	}
	return validateMaterialMetadata(token, result.ContentLength, materialString(result.ContentType))
}

// OpenMaterialObject streams a short-lived private OSS object through New API.
// Only the first 512 bytes are buffered so the image signature can be checked;
// the remainder is copied directly from OSS to the requesting media provider.
func OpenMaterialObject(ctx context.Context, materialID, requestedFileName string) (*MaterialObject, error) {
	cfg, token, client, err := prepareMaterialAccess(materialID, requestedFileName)
	if err != nil {
		return nil, err
	}
	result, err := client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(cfg.Bucket),
		Key:    oss.Ptr(token.ObjectKey),
	})
	if err != nil {
		return nil, fmt.Errorf("material is unavailable: %w", err)
	}

	metadata, err := validateMaterialMetadata(token, result.ContentLength, materialString(result.ContentType))
	if err != nil {
		_ = result.Body.Close()
		return nil, err
	}
	prefix := make([]byte, 512)
	readBytes, readErr := io.ReadFull(result.Body, prefix)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		_ = result.Body.Close()
		return nil, fmt.Errorf("read material signature: %w", readErr)
	}
	prefix = prefix[:readBytes]
	if detected := http.DetectContentType(prefix); detected != metadata.ContentType {
		_ = result.Body.Close()
		return nil, errors.New("uploaded material content does not match its declared image type")
	}

	return &MaterialObject{
		MaterialObjectMetadata: *metadata,
		Body: &materialReadCloser{
			Reader: io.MultiReader(bytes.NewReader(prefix), result.Body),
			Closer: result.Body,
		},
	}, nil
}

// OpenMaterialObjectForUser opens an uploaded image only when it belongs to
// the authenticated user. MCP image editing uses it to forward local reference
// images to the synchronous image-edit endpoint without exposing OSS secrets.
func OpenMaterialObjectForUser(ctx context.Context, userID int, materialID string) (*MaterialObject, error) {
	if userID <= 0 {
		return nil, errors.New("authenticated user is required")
	}
	token, err := decodeMaterialToken(strings.TrimSpace(materialID))
	if err != nil {
		return nil, err
	}
	if token.UserID != userID {
		return nil, errors.New("material does not belong to the authenticated user")
	}
	_, extension, err := normalizeMaterialType("", token.ContentType)
	if err != nil {
		return nil, err
	}
	return OpenMaterialObject(ctx, materialID, "material"+extension)
}

// StoreGeneratedImage keeps a generated original in private OSS and returns a
// short, opaque New API delivery URL when a public base URL is configured. The
// delivery handler redirects browser/Codex downloads to a brief OSS signature,
// with the previous application stream retained as a reliability fallback. A
// direct OSS signed URL remains the fallback when a public base URL is not
// configured. The existing temp-materials lifecycle rule cleans the object
// asynchronously.
func StoreGeneratedImage(ctx context.Context, userID int, payload []byte, mimeType string) (*GeneratedImageAsset, error) {
	if userID <= 0 {
		return nil, errors.New("authenticated user is required")
	}
	if len(payload) == 0 {
		return nil, errors.New("generated image is empty")
	}
	if len(payload) > 32*1024*1024 {
		return nil, errors.New("generated image exceeds the 32 MiB delivery limit")
	}

	mimeType, extension, err := normalizeGeneratedImageType(mimeType, payload)
	if err != nil {
		return nil, err
	}
	cfg, err := loadMaterialStorageConfig()
	if err != nil {
		return nil, err
	}
	client, err := getMaterialClient(cfg)
	if err != nil {
		return nil, err
	}

	objectKey := cfg.Prefix + strconv.Itoa(userID) + "/generated/" + uuid.NewString() + extension
	_, err = client.PutObject(ctx, &oss.PutObjectRequest{
		Bucket:             oss.Ptr(cfg.Bucket),
		Key:                oss.Ptr(objectKey),
		Body:               bytes.NewReader(payload),
		ContentLength:      oss.Ptr(int64(len(payload))),
		ContentType:        oss.Ptr(mimeType),
		ContentDisposition: oss.Ptr("inline"),
		CacheControl:       oss.Ptr("private, max-age=86400"),
		ForbidOverwrite:    oss.Ptr("true"),
	})
	if err != nil {
		return nil, fmt.Errorf("store generated image in OSS: %w", err)
	}

	expiresAt := time.Now().Add(cfg.DownloadTTL)
	if publicURL, ok := buildGeneratedImagePublicURL(
		cfg,
		userID,
		objectKey,
		mimeType,
		int64(len(payload)),
		expiresAt,
	); ok {
		return &GeneratedImageAsset{
			URL:       publicURL,
			ExpiresAt: expiresAt.Unix(),
			MimeType:  mimeType,
		}, nil
	}

	result, err := client.Presign(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(cfg.Bucket),
		Key:    oss.Ptr(objectKey),
	}, oss.PresignExpires(cfg.DownloadTTL))
	if err != nil {
		return nil, fmt.Errorf("create generated image download signature: %w", err)
	}

	return &GeneratedImageAsset{
		URL:       result.URL,
		ExpiresAt: result.Expiration.Unix(),
		MimeType:  mimeType,
	}, nil
}

// PresignGeneratedImageObject turns the short application delivery URL into a
// brief direct OSS download. Only server-generated objects are eligible; user
// uploads keep the existing validated streaming behavior used by providers.
// The signed material token already authenticates the object key, type, size,
// owner, and outer expiry, so no image bytes pass through the application.
func PresignGeneratedImageObject(ctx context.Context, materialID, requestedFileName string) (*GeneratedImageRedirect, error) {
	cfg, token, client, err := prepareMaterialAccess(materialID, requestedFileName)
	if err != nil {
		return nil, err
	}
	generatedPrefix := cfg.Prefix + strconv.Itoa(token.UserID) + "/generated/"
	if !strings.HasPrefix(token.ObjectKey, generatedPrefix) {
		return nil, errors.New("material is not a generated image")
	}
	metadata, err := validateMaterialMetadata(token, token.SizeBytes, token.ContentType)
	if err != nil {
		return nil, err
	}

	remaining := time.Until(time.Unix(token.ExpiresAt, 0))
	if remaining <= 0 {
		return nil, errors.New("material_id has expired")
	}
	presignTTL := 10 * time.Minute
	if cfg.DownloadTTL < presignTTL {
		presignTTL = cfg.DownloadTTL
	}
	if remaining < presignTTL {
		presignTTL = remaining
	}
	result, err := client.Presign(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(cfg.Bucket),
		Key:    oss.Ptr(token.ObjectKey),
	}, oss.PresignExpires(presignTTL))
	if err != nil {
		return nil, fmt.Errorf("create generated image redirect signature: %w", err)
	}
	return &GeneratedImageRedirect{
		URL:                    result.URL,
		MaterialObjectMetadata: *metadata,
	}, nil
}

func buildGeneratedImagePublicURL(
	cfg materialStorageConfig,
	userID int,
	objectKey string,
	contentType string,
	sizeBytes int64,
	expiresAt time.Time,
) (string, bool) {
	if userID <= 0 || strings.TrimSpace(objectKey) == "" || sizeBytes <= 0 || expiresAt.IsZero() {
		return "", false
	}
	token, err := encodeMaterialToken(materialToken{
		Version:     1,
		UserID:      userID,
		ObjectKey:   objectKey,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		ExpiresAt:   expiresAt.Unix(),
	})
	if err != nil {
		return "", false
	}
	return buildMaterialPublicURL(cfg.PublicBaseURL, token, contentType)
}

func normalizeGeneratedImageType(mimeType string, payload []byte) (string, string, error) {
	detected := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(payload), ";")[0]))
	declared := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if strings.HasPrefix(detected, "image/") {
		declared = detected
	}
	extensions := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
		"image/gif":  ".gif",
	}
	extension, ok := extensions[declared]
	if !ok {
		return "", "", errors.New("generated image type is not supported")
	}
	return declared, extension, nil
}

type materialReadCloser struct {
	io.Reader
	io.Closer
}

func prepareMaterialAccess(materialID, requestedFileName string) (materialStorageConfig, materialToken, *oss.Client, error) {
	cfg, err := loadMaterialStorageConfig()
	if err != nil {
		return materialStorageConfig{}, materialToken{}, nil, err
	}
	token, err := decodeMaterialToken(strings.TrimSpace(materialID))
	if err != nil {
		return materialStorageConfig{}, materialToken{}, nil, err
	}
	if token.ExpiresAt < time.Now().Unix() {
		return materialStorageConfig{}, materialToken{}, nil, errors.New("material_id has expired")
	}
	if !strings.HasPrefix(token.ObjectKey, cfg.Prefix+strconv.Itoa(token.UserID)+"/") {
		return materialStorageConfig{}, materialToken{}, nil, errors.New("material object path is invalid")
	}
	_, expectedExtension, err := normalizeMaterialType("", token.ContentType)
	if err != nil || path.Ext(token.ObjectKey) != expectedExtension || path.Ext(requestedFileName) != expectedExtension {
		return materialStorageConfig{}, materialToken{}, nil, errors.New("material file type is invalid")
	}
	client, err := getMaterialClient(cfg)
	if err != nil {
		return materialStorageConfig{}, materialToken{}, nil, err
	}
	return cfg, token, client, nil
}

func validateMaterialMetadata(token materialToken, contentLength int64, contentType string) (*MaterialObjectMetadata, error) {
	declaredType, extension, err := normalizeMaterialType("", token.ContentType)
	if err != nil {
		return nil, errors.New("material image type is invalid")
	}
	storedType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if storedType != declaredType {
		return nil, errors.New("stored material content type does not match its upload request")
	}
	if contentLength <= 0 || contentLength != token.SizeBytes {
		return nil, errors.New("stored material size does not match its upload request")
	}
	return &MaterialObjectMetadata{
		ContentType:   declaredType,
		ContentLength: contentLength,
		FileName:      "material" + extension,
	}, nil
}

func buildMaterialPublicURL(baseURL, materialID, contentType string) (string, bool) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return "", false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()) {
		return "", false
	}
	_, extension, err := normalizeMaterialType("", contentType)
	if err != nil || strings.TrimSpace(materialID) == "" {
		return "", false
	}
	return baseURL + "/v1/materials/content/" + url.PathEscape(materialID) + "/material" + extension, true
}

func materialString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func ScheduleMaterialCleanup(objectKeys []string) {
	if len(objectKeys) == 0 {
		return
	}
	keys := append([]string(nil), objectKeys...)
	cfg, err := loadMaterialStorageConfig()
	if err != nil {
		return
	}
	time.AfterFunc(cfg.CleanupDelay, func() {
		client, clientErr := getMaterialClient(cfg)
		if clientErr != nil {
			logger.LogError(context.Background(), "initialize OSS material cleanup: "+clientErr.Error())
			return
		}
		for _, objectKey := range keys {
			_, deleteErr := client.DeleteObject(context.Background(), &oss.DeleteObjectRequest{
				Bucket: oss.Ptr(cfg.Bucket),
				Key:    oss.Ptr(objectKey),
			})
			if deleteErr != nil {
				logger.LogError(context.Background(), fmt.Sprintf("delete temporary OSS material %s: %s", objectKey, deleteErr.Error()))
			}
		}
	})
}

func normalizeMaterialType(fileName, contentType string) (string, string, error) {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	extensionByType := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
	}
	extension, ok := extensionByType[contentType]
	if !ok {
		return "", "", errors.New("content_type must be image/jpeg, image/png, or image/webp")
	}
	if fileName != "" {
		fileExtension := strings.ToLower(path.Ext(fileName))
		if fileExtension == ".jpeg" {
			fileExtension = ".jpg"
		}
		if fileExtension != "" && fileExtension != extension {
			return "", "", errors.New("file extension does not match content_type")
		}
	}
	return contentType, extension, nil
}

func encodeMaterialToken(token materialToken) (string, error) {
	payload, err := common.Marshal(token)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + common.GenerateHMAC("oss-material:"+encoded), nil
}

func decodeMaterialToken(value string) (materialToken, error) {
	var token materialToken
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return token, errors.New("invalid material_id")
	}
	expected := common.GenerateHMAC("oss-material:" + parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return token, errors.New("invalid material_id signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return token, errors.New("invalid material_id encoding")
	}
	if err := common.Unmarshal(payload, &token); err != nil || token.Version != 1 || token.UserID <= 0 || token.ObjectKey == "" {
		return materialToken{}, errors.New("invalid material_id payload")
	}
	return token, nil
}

func loadMaterialStorageConfig() (materialStorageConfig, error) {
	region := firstMaterialEnv("MATERIAL_OSS_REGION", "OSS_REGION")
	bucket := firstMaterialEnv("MATERIAL_OSS_BUCKET", "OSS_BUCKET")
	if region == "" || bucket == "" {
		return materialStorageConfig{}, errMaterialStorageDisabled
	}
	prefix := firstMaterialEnv("MATERIAL_OSS_PREFIX", "OSS_PREFIX")
	if prefix == "" {
		prefix = defaultMaterialPrefix
	}
	prefix = strings.TrimLeft(prefix, "/")
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if strings.Contains(prefix, "..") {
		return materialStorageConfig{}, errors.New("MATERIAL_OSS_PREFIX is invalid")
	}

	publicBaseURL := firstMaterialEnv("MATERIAL_PUBLIC_BASE_URL", "MATERIAL_OSS_PUBLIC_BASE_URL")
	if publicBaseURL == "" {
		publicBaseURL = system_setting.ServerAddress
	}

	return materialStorageConfig{
		Region:        region,
		Endpoint:      firstMaterialEnv("MATERIAL_OSS_ENDPOINT", "OSS_ENDPOINT"),
		Bucket:        bucket,
		Prefix:        prefix,
		RoleName:      firstMaterialEnv("MATERIAL_OSS_ROLE_NAME", "OSS_ROLE_NAME"),
		PublicBaseURL: publicBaseURL,
		MaxFileSize:   materialEnvInt64("MATERIAL_OSS_MAX_FILE_SIZE", defaultMaterialMaxFileSize),
		UploadTTL:     materialEnvDuration("MATERIAL_OSS_UPLOAD_URL_TTL", defaultMaterialUploadTTL),
		DownloadTTL:   materialEnvDuration("MATERIAL_OSS_DOWNLOAD_URL_TTL", defaultMaterialDownloadTTL),
		TokenTTL:      materialEnvDuration("MATERIAL_OSS_TOKEN_TTL", defaultMaterialTokenTTL),
		CleanupDelay:  materialEnvDuration("MATERIAL_OSS_CLEANUP_DELAY", defaultMaterialCleanupDelay),
	}, nil
}

func getMaterialClient(cfg materialStorageConfig) (*oss.Client, error) {
	configKey := strings.Join([]string{cfg.Region, cfg.Endpoint, cfg.RoleName}, "|")
	materialClientMu.Lock()
	defer materialClientMu.Unlock()
	if materialClient != nil && materialClientConfigKey == configKey {
		return materialClient, nil
	}
	provider := credentials.NewEcsRoleCredentialsProvider(credentials.EcsRamRole(cfg.RoleName))
	ossConfig := oss.LoadDefaultConfig().
		WithCredentialsProvider(provider).
		WithRegion(cfg.Region)
	if cfg.Endpoint != "" {
		ossConfig.WithEndpoint(cfg.Endpoint)
	}
	materialClient = oss.NewClient(ossConfig)
	materialClientConfigKey = configKey
	return materialClient, nil
}

func firstMaterialEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func materialEnvInt64(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func materialEnvDuration(name string, fallback time.Duration) time.Duration {
	seconds := materialEnvInt64(name, int64(fallback/time.Second))
	return time.Duration(seconds) * time.Second
}
