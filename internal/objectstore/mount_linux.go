package objectstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func checkSMBMount(root, source string) error {
	resolved, err := filepath.EvalSymlinks(root)
	if err == nil {
		root = resolved
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	bestLen := -1
	valid := false
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 {
			continue
		}
		left, right := strings.Fields(parts[0]), strings.Fields(parts[1])
		if len(left) < 5 || len(right) < 2 {
			continue
		}
		point := decode.Replace(left[4])
		rel, err := filepath.Rel(point, root)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		if len(point) > bestLen {
			bestLen = len(point)
			valid = (right[0] == "cifs" || right[0] == "smb3") && strings.EqualFold(strings.TrimRight(decode.Replace(right[1]), "/"), strings.TrimRight(source, "/"))
		}
	}
	if !valid {
		return fmt.Errorf("filesystem backend %q requires mounted SMB source %q", root, source)
	}
	return nil
}
