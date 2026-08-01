package service

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaterialTokenRoundTripAndTamperDetection(t *testing.T) {
	original := materialToken{
		Version:     1,
		UserID:      42,
		ObjectKey:   "temp-materials/42/example.png",
		ContentType: "image/png",
		SizeBytes:   1234,
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}
	encoded, err := encodeMaterialToken(original)
	require.NoError(t, err)

	decoded, err := decodeMaterialToken(encoded)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)

	_, err = decodeMaterialToken(encoded + "tampered")
	assert.Error(t, err)
}

func TestNormalizeMaterialType(t *testing.T) {
	contentType, extension, err := normalizeMaterialType("frame.jpeg", "image/jpeg")
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", contentType)
	assert.Equal(t, ".jpg", extension)

	_, _, err = normalizeMaterialType("frame.png", "image/jpeg")
	assert.Error(t, err)
	_, _, err = normalizeMaterialType("frame.gif", "image/gif")
	assert.Error(t, err)
}

func TestBuildMaterialPublicURL(t *testing.T) {
	materialID := "signed_material.token"
	materialURL, ok := buildMaterialPublicURL("https://api.example.com/", materialID, "image/png")
	require.True(t, ok)
	assert.Equal(
		t,
		"https://api.example.com/v1/materials/content/signed_material.token/material.png",
		materialURL,
	)

	_, ok = buildMaterialPublicURL("http://127.0.0.1:3000", materialID, "image/png")
	assert.False(t, ok)
	_, ok = buildMaterialPublicURL("https://api.example.com", materialID, "image/gif")
	assert.False(t, ok)
}

func TestValidateMaterialMetadata(t *testing.T) {
	token := materialToken{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
	}
	metadata, err := validateMaterialMetadata(token, 1024, "image/jpeg; charset=binary")
	require.NoError(t, err)
	assert.Equal(t, "material.jpg", metadata.FileName)

	_, err = validateMaterialMetadata(token, 1024, "application/octet-stream")
	assert.ErrorContains(t, err, "content type")
	_, err = validateMaterialMetadata(token, 512, "image/jpeg")
	assert.ErrorContains(t, err, "size")
}

func TestMaterialImageSignatures(t *testing.T) {
	tests := []struct {
		contentType string
		prefix      string
	}{
		{contentType: "image/jpeg", prefix: "\xff\xd8\xff\xe0\x00\x10JFIF\x00"},
		{contentType: "image/png", prefix: "\x89PNG\r\n\x1a\n"},
		{contentType: "image/webp", prefix: "RIFF\x10\x00\x00\x00WEBPVP8 "},
	}
	for _, test := range tests {
		t.Run(test.contentType, func(t *testing.T) {
			detected := http.DetectContentType([]byte(test.prefix + strings.Repeat("x", 32)))
			assert.Equal(t, test.contentType, detected)
		})
	}
}
