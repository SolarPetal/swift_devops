package deploy

import (
	"os"
)

// tempFileWith 写一个临时文件，返回路径。
func tempFileWith(content string) (string, error) {
	f, err := os.CreateTemp("", "deploy-test-*.bin")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func cleanupTemp(p string) { _ = os.Remove(p) }
