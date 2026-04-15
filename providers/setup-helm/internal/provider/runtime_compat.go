package provider

import "os"

func filepathRemoveAll(path string) error {
	return os.RemoveAll(path)
}

func filepathMkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func runtimeStat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
