//go:build !darwin && !windows

package updater

// Stage is not implemented off macOS and Windows: there is no packaged install.
func Stage(dataDir, assetPath, version string) error {
	return ErrNotPackaged
}

// ApplyStaged is a no-op off macOS and Windows.
func ApplyStaged(dataDir string, reexec bool) error {
	return nil
}
