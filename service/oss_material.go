package service

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

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

type materialToken struct {
	Version     int    `json:"v"`
	UserID      int    `json:"user_id"`
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	ExpiresAt   int64  `json:"expires_at"`
}

type materialStorageConfig struct {
	Region          string
	Endpoint        string
	Bucket          string
	Prefix          string
	RoleName        string
	MaxFileSize     int64
	UploadTTL       time.Duration
	DownloadTTL     time.Duration
	TokenTTL        time.Duration
	CleanupDelay    time.Duration
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
		if head.ContentLength <= 0 || head.ContentLength > cfg.MaxFileSize || head.ContentLength != token.SizeBytes {
			return nil, nil, errors.New("uploaded material size does not match its upload request")
		}

		result, err := client.Presign(ctx, &oss.GetObjectRequest{
			Bucket: oss.Ptr(cfg.Bucket),
			Key:    oss.Ptr(token.ObjectKey),
		}, oss.PresignExpires(cfg.DownloadTTL))
		if err != nil {
			return nil, nil, fmt.Errorf("create OSS download signature: %w", err)
		}
		urls = append(urls, result.URL)
		objectKeys = append(objectKeys, token.ObjectKey)
		seen[token.ObjectKey] = struct{}{}
	}
	return urls, objectKeys, nil
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

	return materialStorageConfig{
		Region:       region,
		Endpoint:     firstMaterialEnv("MATERIAL_OSS_ENDPOINT", "OSS_ENDPOINT"),
		Bucket:       bucket,
		Prefix:       prefix,
		RoleName:     firstMaterialEnv("MATERIAL_OSS_ROLE_NAME", "OSS_ROLE_NAME"),
		MaxFileSize:  materialEnvInt64("MATERIAL_OSS_MAX_FILE_SIZE", defaultMaterialMaxFileSize),
		UploadTTL:    materialEnvDuration("MATERIAL_OSS_UPLOAD_URL_TTL", defaultMaterialUploadTTL),
		DownloadTTL:  materialEnvDuration("MATERIAL_OSS_DOWNLOAD_URL_TTL", defaultMaterialDownloadTTL),
		TokenTTL:     materialEnvDuration("MATERIAL_OSS_TOKEN_TTL", defaultMaterialTokenTTL),
		CleanupDelay: materialEnvDuration("MATERIAL_OSS_CLEANUP_DELAY", defaultMaterialCleanupDelay),
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
