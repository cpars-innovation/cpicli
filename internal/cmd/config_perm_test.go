package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigPermissionError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	p := filepath.Join(t.TempDir(), "cpictl.yaml")
	assert.NoError(t, os.WriteFile(p, []byte("tmn-host: x\n"), 0o600))
	for mode, ok := range map[os.FileMode]bool{0o600: true, 0o644: true, 0o664: true, 0o666: false, 0o602: false} {
		assert.NoError(t, os.Chmod(p, mode))
		err := configPermissionError([]configFile{{path: p}})
		if ok {
			assert.NoError(t, err, "%o", mode)
		} else {
			assert.ErrorContains(t, err, "can be changed by every user", "%o", mode)
		}
	}
}
