//go:build windows

package launcher

// configTrusted answers the unix ownership-and-mode gate on windows, where a
// file's access is governed by ACLs that neither a uid nor a mode word
// describes. It trusts every config, which is how api_key_cmd behaved on
// windows before the gate existed.
func configTrusted(path string) error {
	return nil
}
