package mocktenant

import (
	"os/exec"
	"strings"
	"testing"
)

// Programs that embed the mock tenant (the cpictl gateway's tests) do not
// link SSH: pkg/mocktenant/sshkeys adds it where needed (cpicli#62).
func TestNoSSHWithoutSSHKeys(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "golang.org/x/crypto/ssh") {
			t.Errorf("pkg/mocktenant depends on %s", dep)
		}
	}
}
