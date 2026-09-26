package password

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndCheck(t *testing.T) {
	hash, err := Hash("secret")
	require.NoError(t, err)
	assert.NotEqual(t, "secret", hash)
	assert.True(t, Check(hash, "secret"))
	assert.False(t, Check(hash, "wrong"))
	assert.False(t, Check("not-a-hash", "secret"))

	_, err = Hash(string(make([]byte, MaxLen+1)))
	assert.Error(t, err, "bcrypt rejects passwords longer than MaxLen bytes")
}
