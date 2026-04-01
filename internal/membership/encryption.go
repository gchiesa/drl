package membership

import "github.com/hashicorp/memberlist"

// BuildKeyring creates a memberlist.Keyring from the given secret keys.
// Returns (nil, nil) if the slice is empty (encryption disabled).
// The first key is the primary (encryption) key; the rest are secondary
// (decryption only, for key rotation). All keys must be valid AES lengths
// (16, 24, or 32 bytes).
func BuildKeyring(secretKeys []string) (*memberlist.Keyring, error) {
	if len(secretKeys) == 0 {
		return nil, nil
	}
	keys := make([][]byte, len(secretKeys))
	for i, k := range secretKeys {
		keys[i] = []byte(k)
	}
	return memberlist.NewKeyring(keys, keys[0])
}
