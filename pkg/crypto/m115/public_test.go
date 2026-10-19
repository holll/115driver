package m115

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"invalid-base64":        "!",
		"empty":                 "",
		"short-block":           base64.StdEncoding.EncodeToString(make([]byte, _KeyLength-1)),
		"partial-block":         base64.StdEncoding.EncodeToString(make([]byte, _KeyLength+1)),
		"empty-plaintext":       base64.StdEncoding.EncodeToString(make([]byte, _KeyLength)),
		"multiple-empty-blocks": base64.StdEncoding.EncodeToString(make([]byte, 2*_KeyLength)),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			require.NotPanics(t, func() {
				output, err := Decode(input, Key{})
				require.Error(t, err)
				require.Nil(t, output)
			})
		})
	}
}
