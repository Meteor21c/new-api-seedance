package service

import (
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
