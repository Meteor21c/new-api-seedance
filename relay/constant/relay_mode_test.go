package constant

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPath2RelayModeRecognizesPlaygroundImageEdit(t *testing.T) {
	require.Equal(t, RelayModeImagesEdits, Path2RelayMode("/pg/images/edits"))
}
